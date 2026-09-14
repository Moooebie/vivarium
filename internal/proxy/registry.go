package proxy

import (
	"context"
	"crypto/subtle"
	"net"
	"net/url"
	"strings"
	"sync"

	"vivarium/internal/models"
)

// InstanceStore is the subset of the metadata store the registry needs.
type InstanceStore interface {
	ListInstances() ([]models.Instance, error)
	PutInstance(models.Instance) error
}

// IPResolver resolves a container ID to its address on vivarium-net.
type IPResolver interface {
	IPAddress(ctx context.Context, containerID string) (string, error)
}

// Endpoint is a registered instance endpoint.
type Endpoint struct {
	InstanceID string
	models.InstanceEndpoint
}

// Registry maps container IPs and dummy tokens to instance endpoints.
type Registry struct {
	mu      sync.RWMutex
	byIP    map[string][]Endpoint
	byToken map[string]Endpoint
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		byIP:    map[string][]Endpoint{},
		byToken: map[string]Endpoint{},
	}
}

// Register adds or replaces the endpoints for an instance.
func (r *Registry) Register(inst models.Instance) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removeLocked(inst.ID)
	for _, e := range inst.Endpoints {
		ep := Endpoint{InstanceID: inst.ID, InstanceEndpoint: e}
		if inst.IPAddress != "" {
			r.byIP[inst.IPAddress] = append(r.byIP[inst.IPAddress], ep)
		}
		r.byToken[e.Token] = ep
	}
}

// Unregister removes all endpoints for an instance.
func (r *Registry) Unregister(instanceID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removeLocked(instanceID)
}

func (r *Registry) removeLocked(instanceID string) {
	for ip, eps := range r.byIP {
		kept := eps[:0]
		for _, ep := range eps {
			if ep.InstanceID == instanceID {
				delete(r.byToken, ep.Token)
				continue
			}
			kept = append(kept, ep)
		}
		if len(kept) == 0 {
			delete(r.byIP, ip)
		} else {
			r.byIP[ip] = kept
		}
	}
}

// Match finds the endpoint for a request from ip to host/path. When several
// endpoints share a hostname the longest mock_url path prefix wins. If no
// endpoint's path prefix matches (agents may use a different path than the
// configured mock_url), the host match with the longest common path prefix is
// used so the request is still routed to the upstream.
func (r *Registry) Match(ip, host, path string) (Endpoint, bool) {
	host = stripPort(host)
	r.mu.RLock()
	defer r.mu.RUnlock()

	var best Endpoint
	bestLen := -1
	pathFound := false

	var fallback Endpoint
	bestCommon := -1
	hostCount := 0

	for _, ep := range r.byIP[ip] {
		u, err := url.Parse(ep.MockURL)
		if err != nil || !strings.EqualFold(u.Hostname(), host) {
			continue
		}
		hostCount++
		if pathHasPrefix(path, u.Path) && len(u.Path) > bestLen {
			best = ep
			bestLen = len(u.Path)
			pathFound = true
		}
		common := commonPrefixLen(path, u.Path)
		if common > bestCommon || (common == bestCommon && len(u.Path) > len(fallback.MockURL)) {
			fallback = ep
			bestCommon = common
		}
	}
	if pathFound {
		return best, true
	}
	if hostCount > 0 {
		return fallback, true
	}
	return Endpoint{}, false
}

// commonPrefixLen returns the length of the shared prefix of a and b.
func commonPrefixLen(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}

// ValidateToken reports whether token matches the endpoint's dummy token.
func (r *Registry) ValidateToken(ep Endpoint, token string) bool {
	return subtle.ConstantTimeCompare([]byte(ep.Token), []byte(token)) == 1
}

// Reconcile rebuilds the registry from persisted instances, resolving live
// container addresses.
func (r *Registry) Reconcile(ctx context.Context, st InstanceStore, resolver IPResolver) error {
	r.mu.Lock()
	r.byIP = map[string][]Endpoint{}
	r.byToken = map[string]Endpoint{}
	r.mu.Unlock()

	instances, err := st.ListInstances()
	if err != nil {
		return err
	}
	for _, inst := range instances {
		if inst.Status != models.StatusRunning || inst.ContainerID == "" {
			continue
		}
		if ip, err := resolver.IPAddress(ctx, inst.ContainerID); err == nil && ip != "" {
			inst.IPAddress = ip
			_ = st.PutInstance(inst)
		}
		r.Register(inst)
	}
	return nil
}

func stripPort(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

func pathHasPrefix(path, prefix string) bool {
	if prefix == "" || prefix == "/" {
		return true
	}
	prefix = strings.TrimSuffix(prefix, "/")
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

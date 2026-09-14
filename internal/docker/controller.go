package docker

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"vivarium/internal/models"
)

// InjectedFile is a file written into the guest before start.
type InjectedFile struct {
	Content []byte
	Mode    os.FileMode
}

// Forward is a guest-relay listener/target pair.
type Forward struct {
	Listen string
	Target string
}

// GuestBridgeSpec describes the in-guest TCP relay.
type GuestBridgeSpec struct {
	Binary   []byte
	Path     string
	Forwards []Forward
}

// guestBridgeDefaultPath is used when Path is empty.
const guestBridgeDefaultPath = "/usr/local/bin/vivarium-guestbridge"

// ContainerSpec describes a container to provision.
type ContainerSpec struct {
	Name       string
	Image      string
	User       string
	WorkingDir string
	Cmd        []string
	Env        map[string]string
	Mounts     []models.Mount
	GPUs       []models.GPU
	// MockHosts are hostnames that must resolve to MockHostIP.
	MockHosts []string
	// MockHostIP is the address MockHosts resolve to. Empty means the
	// controller's gateway.
	MockHostIP string
	Resources  []models.Resource
	// CACert, when set, is injected into the container's trust stores.
	CACert []byte
	// Files are written into the guest filesystem before start, keyed by
	// absolute guest path.
	Files map[string]InjectedFile
	// GuestBridge, when set, is injected and started after the container.
	GuestBridge *GuestBridgeSpec
}

// CA certificate destination paths inside the guest.
const (
	GuestCACertPath = "/usr/local/share/ca-certificates/vivarium-ca.crt"
	GuestCAPEMPath  = "/etc/ssl/certs/vivarium-ca.pem"
)

// Controller provides high-level lifecycle operations over the Engine API.
type Controller struct {
	client  *Client
	gateway string
}

// NewController returns a controller bound to the Docker socket.
func NewController(socketPath string) *Controller {
	return &Controller{client: NewClient(socketPath), gateway: NetworkGateway}
}

// NewControllerWithClient returns a controller using a caller-supplied client
// (used in tests).
func NewControllerWithClient(c *Client, gateway string) *Controller {
	if gateway == "" {
		gateway = NetworkGateway
	}
	return &Controller{client: c, gateway: gateway}
}

// Client exposes the underlying API client.
func (c *Controller) Client() *Client { return c.client }

// Ping checks daemon reachability.
func (c *Controller) Ping(ctx context.Context) error { return c.client.Ping(ctx) }

// Version returns the daemon version.
func (c *Controller) Version(ctx context.Context) (string, error) { return c.client.Version(ctx) }

// EnsureNetwork ensures vivarium-net exists.
func (c *Controller) EnsureNetwork(ctx context.Context) (string, error) {
	return c.client.EnsureNetwork(ctx)
}

// CreateAndStart provisions, injects resources into, and starts a container.
// It returns the container ID.
func (c *Controller) CreateAndStart(ctx context.Context, spec ContainerSpec) (string, error) {
	if spec.Image == "" {
		return "", fmt.Errorf("container spec requires an image")
	}
	for _, m := range spec.Mounts {
		if err := m.Validate(); err != nil {
			return "", fmt.Errorf("invalid mount: %w", err)
		}
	}
	req := c.buildCreateRequest(spec)
	id, err := c.client.CreateContainer(ctx, spec.Name, req)
	if err != nil {
		return "", err
	}
	cleanup := func() { _ = c.client.RemoveContainer(ctx, id, true, true) }

	for _, res := range spec.Resources {
		if err := c.injectResource(ctx, id, res); err != nil {
			cleanup()
			return "", err
		}
	}
	if len(spec.CACert) > 0 {
		if err := c.injectCACert(ctx, id, spec.CACert); err != nil {
			cleanup()
			return "", err
		}
	}
	for path, file := range spec.Files {
		mode := int64(file.Mode)
		if mode == 0 {
			mode = 0o644
		}
		if err := c.client.InjectFile(ctx, id, path, file.Content, mode); err != nil {
			cleanup()
			return "", fmt.Errorf("inject %s: %w", path, err)
		}
	}
	if spec.GuestBridge != nil {
		if len(spec.GuestBridge.Binary) == 0 {
			cleanup()
			return "", fmt.Errorf("guest bridge binary is empty")
		}
		if err := c.client.InjectFile(ctx, id, guestBridgePath(spec.GuestBridge), spec.GuestBridge.Binary, 0o755); err != nil {
			cleanup()
			return "", fmt.Errorf("inject guest bridge: %w", err)
		}
	}
	if err := c.client.StartContainer(ctx, id); err != nil {
		cleanup()
		return "", err
	}
	if len(spec.MockHosts) > 0 {
		if err := c.SyncHosts(ctx, id, spec.MockHosts, spec.MockHostIP); err != nil {
			cleanup()
			return "", err
		}
	}
	if len(spec.CACert) > 0 {
		// Best effort: some minimal images lack update-ca-certificates; the
		// SSL_CERT_FILE / REQUESTS_CA_BUNDLE env vars still cover common SDKs.
		_, _ = c.client.Exec(ctx, id, []string{"update-ca-certificates"})
	}
	if spec.GuestBridge != nil {
		if err := c.StartGuestBridge(ctx, id, *spec.GuestBridge); err != nil {
			cleanup()
			return "", err
		}
	}
	return id, nil
}

func guestBridgePath(gb *GuestBridgeSpec) string {
	if gb.Path != "" {
		return gb.Path
	}
	return guestBridgeDefaultPath
}

// StartGuestBridge launches the in-guest relay (as root) and waits until its
// first listener accepts connections.
func (c *Controller) StartGuestBridge(ctx context.Context, id string, gb GuestBridgeSpec) error {
	if len(gb.Forwards) == 0 {
		return fmt.Errorf("guest bridge has no forwards")
	}
	path := guestBridgePath(&gb)
	args := []string{path}
	for _, f := range gb.Forwards {
		args = append(args, "-forward", f.Listen+"="+f.Target)
	}
	if err := c.client.ExecDetached(ctx, id, "root", args); err != nil {
		return fmt.Errorf("start guest bridge: %w", err)
	}
	for i := 0; i < 25; i++ {
		code, _, err := c.client.ExecStatus(ctx, id, []string{path, "-check", gb.Forwards[0].Listen})
		if err == nil && code == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("guest bridge did not become ready on %s", gb.Forwards[0].Listen)
}

// connectShell is the login shell launched for each interactive session. It
// prefers bash when the image provides it and falls back to POSIX sh.
var connectShell = []string{"/bin/sh", "-c", "if command -v bash >/dev/null 2>&1; then exec bash -l; else exec sh; fi"}

// Connect opens a new interactive shell inside a running container. Unlike
// container attach (which shares PID 1 stdio), each call creates an independent
// exec session, so multiple clients get separate shells. It returns the raw
// stream and the exec ID used for subsequent resizes. env adds (and overrides)
// environment variables for the session, so endpoint changes do not require
// recreating the container.
func (c *Controller) Connect(ctx context.Context, id string, cols, rows int, env map[string]string) (io.ReadWriteCloser, string, error) {
	execID, err := c.client.CreateExec(ctx, id, connectShell, true, envSlice(env))
	if err != nil {
		return nil, "", err
	}
	if cols > 0 && rows > 0 {
		// Best effort: the session still works if the initial resize fails.
		_ = c.client.ResizeExec(ctx, execID, rows, cols)
	}
	stream, err := c.client.StartExec(ctx, execID, true)
	if err != nil {
		return nil, "", err
	}
	return stream, execID, nil
}

// ResizeExec sets the TTY size of an interactive exec session.
func (c *Controller) ResizeExec(ctx context.Context, execID string, height, width int) error {
	return c.client.ResizeExec(ctx, execID, height, width)
}

// hostMarker tags the /etc/hosts lines Vivarium manages so they can be replaced
// without disturbing the container's own entries.
const hostMarker = "# vivarium"

// SyncHosts replaces Vivarium's managed /etc/hosts block with the given mock
// hosts mapped to ip. Docker regenerates /etc/hosts on (re)start, so this is
// applied after create and after every start. It never unlinks the file (which
// is a bind mount) — it writes in place by truncation. Runs as root.
func (c *Controller) SyncHosts(ctx context.Context, id string, hosts []string, ip string) error {
	if ip == "" {
		ip = "127.0.0.1"
	}
	var entries strings.Builder
	for _, h := range hosts {
		h = sanitizeHost(h)
		if h == "" {
			continue
		}
		entries.WriteString("printf '%s %s " + hostMarker + "\\n' '" + ip + "' '" + h + "' >> \"$t\"\n")
	}
	script := "set -e\n" +
		"f=/etc/hosts\n" +
		"t=/tmp/.vivarium-hosts.$$\n" +
		"grep -v '" + hostMarker + "$' \"$f\" > \"$t\" || true\n" +
		entries.String() +
		"cat \"$t\" > \"$f\"\n" +
		"rm -f \"$t\"\n"
	code, out, err := c.client.ExecStatus(ctx, id, []string{"/bin/sh", "-c", script})
	if err != nil {
		return fmt.Errorf("sync hosts: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("sync hosts: exit %d: %s", code, strings.TrimSpace(out))
	}
	return nil
}

// sanitizeHost keeps only characters valid in a hostname, returning "" for
// anything unusable.
func sanitizeHost(h string) string {
	h = strings.TrimSpace(h)
	var b strings.Builder
	for _, r := range h {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-':
			b.WriteRune(r)
		default:
			return ""
		}
	}
	return b.String()
}

// injectCACert writes the CA certificate into the guest trust locations.
func (c *Controller) injectCACert(ctx context.Context, id string, certPEM []byte) error {
	if err := c.client.InjectFile(ctx, id, GuestCACertPath, certPEM, 0o644); err != nil {
		return fmt.Errorf("inject ca cert: %w", err)
	}
	if err := c.client.InjectFile(ctx, id, GuestCAPEMPath, certPEM, 0o644); err != nil {
		return fmt.Errorf("inject ca pem: %w", err)
	}
	return nil
}

func (c *Controller) buildCreateRequest(spec ContainerSpec) containerCreateRequest {
	mounts := make([]mountSpec, 0, len(spec.Mounts))
	for _, m := range spec.Mounts {
		mounts = append(mounts, mountSpec{
			Type:     "bind",
			Source:   m.HostPath,
			Target:   m.GuestPath,
			ReadOnly: m.Mode == models.MountReadOnly,
		})
	}
	// Mock host entries are applied at runtime via SyncHosts (after create and
	// after each start) so endpoints can change without recreating the container.
	hc := &hostConfig{
		Mounts:      mounts,
		NetworkMode: NetworkName,
	}
	if len(spec.GPUs) > 0 {
		hc.Devices = []deviceMapping{
			{PathOnHost: "/dev/kfd", PathInContainer: "/dev/kfd", CgroupPermissions: "rwm"},
			{PathOnHost: "/dev/dri", PathInContainer: "/dev/dri", CgroupPermissions: "rwm"},
		}
		hc.GroupAdd = gpuGroupAdd()
		hc.SecurityOpt = []string{"seccomp=unconfined"}
		hc.IpcMode = "host"
	}
	return containerCreateRequest{
		Image:      spec.Image,
		Cmd:        spec.Cmd,
		Env:        envSlice(spec.Env),
		User:       spec.User,
		WorkingDir: spec.WorkingDir,
		Tty:        true,
		OpenStdin:  true,
		HostConfig: hc,
	}
}

// gpuGroupAdd returns supplementary groups for GPU access as numeric GIDs.
// Numeric GIDs are used because --group-add resolves names against the
// container's /etc/group, which may not define "render" (or "video").
func gpuGroupAdd() []string {
	paths := []string{"/dev/kfd"}
	if matches, _ := filepath.Glob("/dev/dri/renderD*"); len(matches) > 0 {
		paths = append(paths, matches...)
	}
	return gpuGroups(paths, groupGID)
}

// groupGID resolves a group name to its numeric GID on the host.
func groupGID(name string) (uint32, bool) {
	g, err := user.LookupGroup(name)
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(g.Gid)
	if err != nil {
		return 0, false
	}
	return uint32(n), true
}

// gpuGroups collects deduplicated numeric GIDs for the named groups (when
// resolvable) and the owning groups of the given device paths.
func gpuGroups(devicePaths []string, lookup func(string) (uint32, bool)) []string {
	var out []string
	seen := map[uint32]struct{}{}
	add := func(gid uint32) {
		if _, ok := seen[gid]; ok {
			return
		}
		seen[gid] = struct{}{}
		out = append(out, strconv.Itoa(int(gid)))
	}
	for _, name := range []string{"video", "render"} {
		if gid, ok := lookup(name); ok {
			add(gid)
		}
	}
	for _, p := range devicePaths {
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			add(st.Gid)
		}
	}
	return out
}

// Halt stops a running container.
func (c *Controller) Halt(ctx context.Context, id string, timeoutSec int) error {
	return c.client.StopContainer(ctx, id, timeoutSec)
}

// Remove stops (if needed) and removes a container.
func (c *Controller) Remove(ctx context.Context, id string) error {
	return c.client.RemoveContainer(ctx, id, true, true)
}

// Status returns the current instance status for a container ID.
func (c *Controller) Status(ctx context.Context, id string) (models.InstanceStatus, error) {
	if id == "" {
		return models.StatusHalted, nil
	}
	inspect, err := c.client.InspectContainer(ctx, id, false)
	if err != nil {
		if IsNotFound(err) {
			return models.StatusError, nil
		}
		return "", err
	}
	if inspect.State == nil {
		return models.StatusError, nil
	}
	switch inspect.State.Status {
	case "running", "paused", "restarting":
		return models.StatusRunning, nil
	case "created":
		return models.StatusBuilding, nil
	case "exited":
		return models.StatusHalted, nil
	case "dead":
		return models.StatusError, nil
	default:
		return models.StatusHalted, nil
	}
}

// IPAddress returns the container IP on vivarium-net.
func (c *Controller) IPAddress(ctx context.Context, id string) (string, error) {
	inspect, err := c.client.InspectContainer(ctx, id, false)
	if err != nil {
		return "", err
	}
	if inspect.NetworkSettings == nil {
		return "", nil
	}
	if ep, ok := inspect.NetworkSettings.Networks[NetworkName]; ok && ep != nil {
		return ep.IPAddress, nil
	}
	return inspect.NetworkSettings.IPAddress, nil
}

// DiskUsage returns the writable-layer size of a container in bytes.
func (c *Controller) DiskUsage(ctx context.Context, id string) (int64, error) {
	inspect, err := c.client.InspectContainer(ctx, id, true)
	if err != nil {
		return 0, err
	}
	return inspect.SizeRw, nil
}

// InjectResource copies a host file into a running/stopped container.
func (c *Controller) InjectResource(ctx context.Context, id string, res models.Resource) error {
	return c.injectResource(ctx, id, res)
}

func (c *Controller) injectResource(ctx context.Context, id string, res models.Resource) error {
	if err := res.Validate(); err != nil {
		return err
	}
	content, err := os.ReadFile(res.HostSourcePath)
	if err != nil {
		return fmt.Errorf("read resource %s: %w", res.HostSourcePath, err)
	}
	mode, err := ParseFileMode(res.FileMode)
	if err != nil {
		return err
	}
	return c.client.InjectFile(ctx, id, res.GuestTargetPath, content, mode)
}

// BuildStandardImage builds one of the embedded standard images.
func (c *Controller) BuildStandardImage(ctx context.Context, key, tag string, logs io.Writer) error {
	dockerfile, err := StandardDockerfile(key)
	if err != nil {
		return err
	}
	return c.client.BuildFromDockerfile(ctx, dockerfile, tag, logs)
}

// ImageExists reports whether an image reference is present locally.
func (c *Controller) ImageExists(ctx context.Context, ref string) (bool, int64, error) {
	return c.client.ImageExists(ctx, ref)
}

// envSlice renders a map of environment variables as sorted KEY=VALUE pairs.
func envSlice(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(env))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}

// SanitizeContainerName converts a display name into a valid Docker name.
func SanitizeContainerName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '.', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	s := strings.Trim(b.String(), "-.")
	// Docker requires container names to be at least two characters.
	if len(s) == 1 {
		s = "v" + s
	}
	if len(s) < 2 {
		return "vivarium-instance"
	}
	return s
}

package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"vivarium/internal/keyring"
	"vivarium/internal/models"
)

// SecretProvider resolves a real API secret by key ID.
type SecretProvider interface {
	Secret(keyID string) (string, error)
}

// Config configures a proxy Server.
type Config struct {
	CA       *CA
	Registry *Registry
	Secrets  SecretProvider
	TLSAddr  string
	HTTPAddr string
	Logger   *log.Logger
	// FlushInterval controls response flushing. The zero value means immediate
	// flushing (-1), which is required for SSE.
	FlushInterval time.Duration
	// UpstreamTLSConfig optionally overrides the upstream TLS configuration
	// (used in tests to trust a mock upstream).
	UpstreamTLSConfig *tls.Config
}

// Server is the host-bridge reverse proxy.
type Server struct {
	cfg     Config
	limiter *limiter
	proxy   *httputil.ReverseProxy
	tlsLn   net.Listener
	httpLn  net.Listener
	tlsSrv  *http.Server
	httpSrv *http.Server
}

// NewServer builds a proxy Server.
func NewServer(cfg Config) *Server {
	if cfg.Logger == nil {
		cfg.Logger = log.New(io.Discard, "", 0)
	}
	if cfg.FlushInterval == 0 {
		cfg.FlushInterval = -1
	}
	s := &Server{cfg: cfg, limiter: newLimiter()}
	s.proxy = s.newReverseProxy()
	return s
}

// Start binds the listeners and begins serving. It returns once listeners are
// established; serving continues until Close or ctx cancellation.
func (s *Server) Start(ctx context.Context) error {
	if s.cfg.CA == nil || s.cfg.Registry == nil {
		return errors.New("proxy requires a CA and registry")
	}
	if s.cfg.TLSAddr != "" {
		ln, err := net.Listen("tcp", s.cfg.TLSAddr)
		if err != nil {
			return err
		}
		tlsLn := tls.NewListener(ln, &tls.Config{
			GetCertificate: s.cfg.CA.GetCertificate,
			MinVersion:     tls.VersionTLS12,
		})
		s.tlsLn = tlsLn
		s.tlsSrv = &http.Server{Handler: s, ReadHeaderTimeout: 15 * time.Second}
		go s.serve(s.tlsSrv, tlsLn)
	}
	if s.cfg.HTTPAddr != "" {
		ln, err := net.Listen("tcp", s.cfg.HTTPAddr)
		if err != nil {
			s.Close()
			return err
		}
		s.httpLn = ln
		s.httpSrv = &http.Server{Handler: s, ReadHeaderTimeout: 15 * time.Second}
		go s.serve(s.httpSrv, ln)
	}
	go func() {
		<-ctx.Done()
		_ = s.Close()
	}()
	return nil
}

func (s *Server) serve(srv *http.Server, ln net.Listener) {
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		s.cfg.Logger.Printf("proxy: serve: %v", err)
	}
}

// Close stops the listeners.
func (s *Server) Close() error {
	var errs []error
	for _, srv := range []*http.Server{s.tlsSrv, s.httpSrv} {
		if srv != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			if err := srv.Shutdown(ctx); err != nil {
				errs = append(errs, err)
			}
			cancel()
		}
	}
	return errors.Join(errs...)
}

// TLSAddr returns the bound TLS listener address, if any.
func (s *Server) TLSAddr() string {
	if s.tlsLn == nil {
		return ""
	}
	return s.tlsLn.Addr().String()
}

// HTTPAddr returns the bound plain listener address, if any.
func (s *Server) HTTPAddr() string {
	if s.httpLn == nil {
		return ""
	}
	return s.httpLn.Addr().String()
}

type proxyCtxKey struct{}

type proxyRequest struct {
	endpoint Endpoint
	secret   string
	target   *url.URL
}

func (s *Server) newReverseProxy() *httputil.ReverseProxy {
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		TLSClientConfig:       s.cfg.UpstreamTLSConfig,
	}
	return &httputil.ReverseProxy{
		Transport:     transport,
		FlushInterval: s.cfg.FlushInterval,
		Rewrite: func(pr *httputil.ProxyRequest) {
			prReq, ok := pr.In.Context().Value(proxyCtxKey{}).(*proxyRequest)
			if !ok {
				return
			}
			target := cloneURL(prReq.target)
			pr.Out.URL = target
			pr.Out.Host = target.Host
			pr.Out.Header.Del("Authorization")
			pr.Out.Header.Del("x-api-key")
			applyAuth(pr.Out, prReq.endpoint, prReq.secret)
			pr.Out.Header.Del("X-Forwarded-For")
			pr.Out.Header.Del("X-Forwarded-Host")
			pr.Out.Header.Del("X-Forwarded-Proto")
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			s.cfg.Logger.Printf("proxy: upstream error: %v", err)
			http.Error(w, "upstream error", http.StatusBadGateway)
		},
	}
}

// ServeHTTP implements the proxy pipeline.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	ep, ok := s.cfg.Registry.Match(ip, r.Host, r.URL.Path)
	if !ok {
		http.Error(w, "no matching endpoint for request", http.StatusForbidden)
		return
	}
	if !s.cfg.Registry.ValidateToken(ep, extractToken(r)) {
		http.Error(w, "invalid credential", http.StatusUnauthorized)
		return
	}
	if allowed, retry := s.limiter.allow(ep.KeyID, ep.RateLimitRPM); !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retry.Seconds()))))
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}
	if s.cfg.Secrets == nil {
		http.Error(w, "credential store unavailable", http.StatusServiceUnavailable)
		return
	}
	secret, err := s.cfg.Secrets.Secret(ep.KeyID)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, keyring.ErrLocked) || errors.Is(err, keyring.ErrUnavailable) {
			status = http.StatusServiceUnavailable
		}
		s.cfg.Logger.Printf("proxy: credential %q unavailable for %s: %v", ep.KeyID, ep.MockURL, err)
		http.Error(w, "credential unavailable: "+err.Error(), status)
		return
	}
	target, err := buildTarget(ep, r.URL)
	if err != nil {
		http.Error(w, "invalid upstream target", http.StatusBadGateway)
		return
	}
	ctx := context.WithValue(r.Context(), proxyCtxKey{}, &proxyRequest{
		endpoint: ep,
		secret:   secret,
		target:   target,
	})
	s.proxy.ServeHTTP(w, r.WithContext(ctx))
}

func applyAuth(out *http.Request, ep Endpoint, secret string) {
	if ep.ProviderType == models.ProviderAnthropic {
		out.Header.Set("x-api-key", secret)
		if out.Header.Get("anthropic-version") == "" {
			out.Header.Set("anthropic-version", "2023-06-01")
		}
		return
	}
	out.Header.Set("Authorization", "Bearer "+secret)
}

func extractToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if len(auth) >= 7 && strings.EqualFold(auth[:7], "bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	return r.Header.Get("x-api-key")
}

func buildTarget(ep Endpoint, reqURL *url.URL) (*url.URL, error) {
	base, err := url.Parse(ep.BaseURL)
	if err != nil {
		return nil, err
	}
	mock, err := url.Parse(ep.MockURL)
	if err != nil {
		return nil, err
	}
	rest := strings.TrimPrefix(reqURL.Path, strings.TrimSuffix(mock.Path, "/"))
	target := *base
	target.Path = singleJoin(base.Path, rest)
	target.RawQuery = reqURL.RawQuery
	return &target, nil
}

func singleJoin(a, b string) string {
	if a == "" {
		a = "/"
	}
	if b == "" {
		return a
	}
	return strings.TrimSuffix(a, "/") + "/" + strings.TrimPrefix(b, "/")
}

func cloneURL(u *url.URL) *url.URL {
	c := *u
	return &c
}

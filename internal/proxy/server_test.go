package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"vivarium/internal/keyring"
	"vivarium/internal/models"
)

type fakeSecrets map[string]string

func (f fakeSecrets) Secret(keyID string) (string, error) {
	if s, ok := f[keyID]; ok {
		return s, nil
	}
	return "", keyring.ErrNotFound
}

func startProxy(t *testing.T, ca *CA, upstream *httptest.Server, rpm int) *Server {
	t.Helper()
	pool := x509.NewCertPool()
	pool.AddCert(upstream.Certificate())
	reg := NewRegistry()
	reg.Register(testInstance("i1", "127.0.0.1", models.InstanceEndpoint{
		KeyID: "k1", ProviderType: models.ProviderOpenAI,
		BaseURL: upstream.URL + "/v1", MockURL: "https://api.openai.com/v1",
		Token: "viv-tok-1", RateLimitRPM: rpm,
	}))
	srv := NewServer(Config{
		CA: ca, Registry: reg, Secrets: fakeSecrets{"k1": "sk-real"},
		TLSAddr: "127.0.0.1:0", UpstreamTLSConfig: &tls.Config{RootCAs: pool},
	})
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

func guestClient(ca *CA, srv *Server) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", srv.TLSAddr())
		},
		TLSClientConfig: &tls.Config{ServerName: "api.openai.com", RootCAs: ca.RootPool()},
	}}
}

func TestProxyRewritesCredentialAndPath(t *testing.T) {
	var mu sync.Mutex
	var gotAuth, gotPath string
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	ca := testCA(t)
	srv := startProxy(t, ca, upstream, 0)
	client := guestClient(ca, srv)

	req, _ := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/chat/completions",
		strings.NewReader(`{"model":"x"}`))
	req.Header.Set("Authorization", "Bearer viv-tok-1")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotAuth != "Bearer sk-real" {
		t.Fatalf("upstream auth = %q, want real key", gotAuth)
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("upstream path = %q", gotPath)
	}
}

func TestProxyRejectsBadToken(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream must not be reached")
	}))
	defer upstream.Close()
	ca := testCA(t)
	srv := startProxy(t, ca, upstream, 0)
	client := guestClient(ca, srv)

	req, _ := http.NewRequest(http.MethodGet, "https://api.openai.com/v1/models", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestProxyRejectsUnknownHost(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream must not be reached")
	}))
	defer upstream.Close()
	ca := testCA(t)
	srv := startProxy(t, ca, upstream, 0)
	client := guestClient(ca, srv)

	req, _ := http.NewRequest(http.MethodGet, "https://evil.example/v1/models", nil)
	req.Header.Set("Authorization", "Bearer viv-tok-1")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestProxyRateLimits(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer upstream.Close()
	ca := testCA(t)
	srv := startProxy(t, ca, upstream, 1)
	client := guestClient(ca, srv)

	do := func() int {
		req, _ := http.NewRequest(http.MethodGet, "https://api.openai.com/v1/models", nil)
		req.Header.Set("Authorization", "Bearer viv-tok-1")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := do(); code != http.StatusOK {
		t.Fatalf("first request = %d", code)
	}
	if code := do(); code != http.StatusTooManyRequests {
		t.Fatalf("second request = %d, want 429", code)
	}
}

func TestProxyStreamsSSE(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		io.WriteString(w, "data: one\n\n")
		fl.Flush()
		<-release
		io.WriteString(w, "data: two\n\n")
		fl.Flush()
	}))
	defer upstream.Close()
	ca := testCA(t)
	srv := startProxy(t, ca, upstream, 0)
	client := guestClient(ca, srv)

	req, _ := http.NewRequest(http.MethodGet, "https://api.openai.com/v1/stream", nil)
	req.Header.Set("Authorization", "Bearer viv-tok-1")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	lineCh := make(chan string, 1)
	go func() {
		line, _ := reader.ReadString('\n')
		lineCh <- line
	}()
	select {
	case line := <-lineCh:
		if !strings.Contains(line, "one") {
			t.Fatalf("first chunk = %q", line)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first SSE chunk was buffered by the proxy")
	}
	close(release)
	rest, _ := io.ReadAll(reader)
	if !strings.Contains(string(rest), "two") {
		t.Fatalf("missing second chunk: %q", rest)
	}
}

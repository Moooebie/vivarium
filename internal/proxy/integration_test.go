//go:build integration

package proxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"vivarium/internal/docker"
	"vivarium/internal/models"
)

// TestIntegrationProxyEndToEnd drives a real container through the host bridge
// to a host mock upstream. Enable with:
//
//	VIVARIUM_INTEGRATION=1 go test -tags=integration ./internal/proxy/
func TestIntegrationProxyEndToEnd(t *testing.T) {
	if os.Getenv("VIVARIUM_INTEGRATION") == "" {
		t.Skip("set VIVARIUM_INTEGRATION=1 to run Docker integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	dc := docker.NewController(docker.DefaultSocket)
	if err := dc.Ping(ctx); err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	if _, err := dc.EnsureNetwork(ctx); err != nil {
		t.Fatalf("ensure network: %v", err)
	}
	const image = "vivarium/ubuntu-24.04-opencode:latest"
	if ok, _, _ := dc.ImageExists(ctx, image); !ok {
		t.Skipf("image %s not present locally", image)
	}

	var mu sync.Mutex
	var gotAuth, gotPath string
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[]}`)
	}))
	defer upstream.Close()

	ca := testCA(t)
	pool := x509.NewCertPool()
	pool.AddCert(upstream.Certificate())

	reg := NewRegistry()
	srv := NewServer(Config{
		CA: ca, Registry: reg, Secrets: fakeSecrets{"k1": "sk-real"},
		TLSAddr: "172.28.0.1:0", UpstreamTLSConfig: &tls.Config{RootCAs: pool},
	})
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	defer srv.Close()
	_, port, err := net.SplitHostPort(srv.TLSAddr())
	if err != nil {
		t.Fatal(err)
	}

	token := "viv-tok-integration"
	endpoint := models.InstanceEndpoint{
		KeyID: "k1", ProviderType: models.ProviderOpenAI,
		BaseURL: upstream.URL + "/v1", MockURL: "https://api.openai.com/v1",
		Token: token,
	}

	name := fmt.Sprintf("vivarium-proxy-it-%d", time.Now().UnixNano())
	id, err := dc.CreateAndStart(ctx, docker.ContainerSpec{
		Name:      name,
		Image:     image,
		Cmd:       []string{"sleep", "120"},
		Env:       map[string]string{"OPENAI_API_BASE": "https://api.openai.com:" + port + "/v1"},
		MockHosts: []string{"api.openai.com"},
		// This test drives the host proxy directly (no guest relay), so the mock
		// host points at the gateway rather than 127.0.0.1.
		MockHostIP: docker.NetworkGateway,
		CACert:     ca.CertPEM(),
	})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	defer func() { _ = dc.Remove(ctx, id) }()

	ip, err := dc.IPAddress(ctx, id)
	if err != nil || ip == "" {
		t.Fatalf("resolve container ip: %v (%q)", err, ip)
	}
	reg.Register(models.Instance{
		ID: "it", ContainerID: id, Name: name, Status: models.StatusRunning,
		BaseImageTag: image, IPAddress: ip, Endpoints: []models.InstanceEndpoint{endpoint},
	})

	out, err := dc.Client().Exec(ctx, id, []string{
		"curl", "-sS", "-X", "POST",
		"https://api.openai.com:" + port + "/v1/chat/completions",
		"-H", "Authorization: Bearer " + token,
		"-H", "Content-Type: application/json",
		"-d", `{"model":"x"}`,
	})
	if err != nil {
		t.Fatalf("curl: %v (output %q)", err, out)
	}
	if !strings.Contains(out, "choices") {
		t.Fatalf("unexpected curl output: %q", out)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotAuth != "Bearer sk-real" {
		t.Fatalf("upstream auth = %q, want rewritten real key", gotAuth)
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("upstream path = %q", gotPath)
	}
}

//go:build integration

// Package e2e contains full-stack integration tests that exercise the daemon,
// the host bridge, and a real agent container end to end.
package e2e

import (
	"context"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"vivarium/internal/api"
	"vivarium/internal/apitypes"
	"vivarium/internal/auth"
	"vivarium/internal/client"
	"vivarium/internal/daemon"
	"vivarium/internal/docker"
	"vivarium/internal/models"
	"vivarium/internal/paths"
	"vivarium/internal/proxy"
	"vivarium/internal/store"
)

// TestDeepSeekAgentHelloWorld provisions an OpenCode container, relies on the
// transparent DNS hijack + guest relay (no base-URL injection), and asks the
// agent to say "hello world".
//
// Enable with:
//
//	VIVARIUM_INTEGRATION=1 VIVARIUM_KEYS_FILE=../.api_keys/keys.txt \
//	  go test -tags=integration ./internal/e2e/
func TestDeepSeekAgentHelloWorld(t *testing.T) {
	if os.Getenv("VIVARIUM_INTEGRATION") == "" {
		t.Skip("set VIVARIUM_INTEGRATION=1 to run the end-to-end agent test")
	}
	keysFile := os.Getenv("VIVARIUM_KEYS_FILE")
	if keysFile == "" {
		keysFile = "../../../.api_keys/keys.txt"
	}
	secret := readKey(keysFile, "deepseek")
	if secret == "" {
		t.Skipf("no deepseek key in %s", keysFile)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dir := t.TempDir()
	st := store.New(filepath.Join(dir, "data"))
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	am := auth.NewManager(st, filepath.Join(dir, "data", "vault.enc"))
	if err := am.Setup(models.SecretVault, "pw"); err != nil {
		t.Fatal(err)
	}

	dc := docker.NewController(docker.DefaultSocket)
	if _, err := dc.EnsureNetwork(ctx); err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	if ok, _, _ := dc.ImageExists(ctx, "vivarium/ubuntu-24.04-opencode:latest"); !ok {
		t.Skip("opencode image not present locally")
	}

	ca, err := proxy.LoadOrCreate(filepath.Join(dir, "certs"))
	if err != nil {
		t.Fatal(err)
	}
	registry := proxy.NewRegistry()
	ps := proxy.NewServer(proxy.Config{
		CA: ca, Registry: registry, Secrets: am,
		TLSAddr: "172.28.0.1:0", HTTPAddr: "172.28.0.1:0",
		Logger: log.New(io.Discard, "", 0),
	})
	if err := ps.Start(ctx); err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	defer ps.Close()
	tlsPort, httpPort := portOf(ps.TLSAddr()), portOf(ps.HTTPAddr())

	srv := api.NewServer(st, am, dc, paths.Env{XDGDataHome: dir, UID: os.Getuid()})
	srv.SetRegistry(registry)
	srv.SetProxyBinding(api.ProxyBinding{CACertPEM: ca.CertPEM(), TLSPort: tlsPort, HTTPPort: httpPort})

	socket := filepath.Join(dir, "viv.sock")
	d := daemon.New(daemon.Options{
		SocketPath: socket,
		LockPath:   filepath.Join(dir, "daemon.lock"),
		Server:     srv,
	})
	go func() { _ = d.Run(ctx) }()

	cli := client.New(socket)
	waitReady(ctx, t, cli)

	key, err := cli.CreateAPIKey(ctx, apitypes.APIKeyPayload{
		APIKey: models.APIKey{
			Name: "DeepSeek E2E", ProviderType: models.ProviderDeepSeek,
			BaseURL: "https://api.deepseek.com/v1", MockURL: "https://api.deepseek.com/v1",
		},
		Secret: secret,
	})
	if err != nil {
		t.Fatalf("store key: %v", err)
	}
	recipe := models.Recipe{Name: "e2e", APIEndpoints: []models.APIKey{key}}
	inst, err := cli.CreateInstance(ctx, apitypes.InstanceCreateRequest{
		Name:         "deepseek-e2e",
		BaseImageTag: "vivarium/ubuntu-24.04-opencode:latest",
		Recipe:       &recipe,
	})
	if err != nil {
		t.Fatalf("create instance: %v", err)
	}
	defer func() { _ = cli.DeleteInstance(context.Background(), inst.ID) }()

	// The agent must have no base URL injected and must resolve the mock host to
	// the loopback relay.
	for k := range inst.EnvVars {
		if strings.Contains(k, "BASE_URL") {
			t.Fatalf("base URL env var injected: %s", k)
		}
	}

	// Resolve the agent binary robustly (some images only ship it under
	// ~/.opencode/bin without a PATH symlink).
	out, err := dc.Client().Exec(ctx, inst.ContainerID, []string{
		"sh", "-c",
		`if command -v opencode >/dev/null 2>&1; then OC=opencode; ` +
			`elif [ -x /root/.opencode/bin/opencode ]; then OC=/root/.opencode/bin/opencode; ` +
			`else echo "opencode not found"; exit 127; fi; ` +
			`exec "$OC" run -m deepseek/deepseek-v4-flash "Reply with exactly: hello world"`,
	})
	if err != nil {
		t.Fatalf("opencode run: %v (output %q)", err, out)
	}
	if !strings.Contains(strings.ToLower(out), "hello world") {
		t.Fatalf("agent did not reply 'hello world': %q", out)
	}
}

func portOf(addr string) int {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(port)
	return n
}

func waitReady(ctx context.Context, t *testing.T, cli *client.Client) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := cli.Ping(ctx); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("daemon did not become ready")
}

func readKey(path, provider string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, provider+"=") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, provider+"="))
		value = strings.Trim(value, "'\"")
		return value
	}
	return ""
}

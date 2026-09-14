//go:build integration

package docker

import (
	"context"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"vivarium/internal/models"
)

// TestIntegrationContainerLifecycle exercises the real Docker daemon. Enable it
// with: VIVARIUM_INTEGRATION=1 go test -tags=integration ./internal/docker
func TestIntegrationContainerLifecycle(t *testing.T) {
	if os.Getenv("VIVARIUM_INTEGRATION") == "" {
		t.Skip("set VIVARIUM_INTEGRATION=1 to run Docker integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	c := NewController(DefaultSocket)
	if err := c.Ping(ctx); err != nil {
		t.Skipf("docker daemon unavailable: %v", err)
	}
	if _, err := c.EnsureNetwork(ctx); err != nil {
		t.Fatalf("ensure network: %v", err)
	}
	net, err := c.Client().InspectNetwork(ctx, NetworkName)
	if err != nil {
		t.Fatalf("inspect network: %v", err)
	}
	if net.Driver != "bridge" {
		t.Fatalf("network driver = %q", net.Driver)
	}

	dir := t.TempDir()
	name := "vivarium-it-" + strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "")
	id, err := c.CreateAndStart(ctx, ContainerSpec{
		Name:      name,
		Image:     "ubuntu:24.04",
		Cmd:       []string{"sleep", "60"},
		Mounts:    []models.Mount{{HostPath: dir, GuestPath: "/mnt/test", Mode: models.MountReadOnly}},
		MockHosts: []string{"api.openai.com"},
	})
	if err != nil {
		t.Fatalf("create and start: %v", err)
	}
	defer func() { _ = c.Remove(ctx, id) }()

	inspect, err := c.Client().InspectContainer(ctx, id, false)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if inspect.HostConfig == nil {
		t.Fatal("missing host config")
	}
	foundHost := false
	hosts, err := c.Client().Exec(ctx, id, []string{"grep", "api.openai.com", "/etc/hosts"})
	if err == nil && strings.Contains(hosts, "127.0.0.1") {
		foundHost = true
	}
	if !foundHost {
		t.Fatalf("expected a managed /etc/hosts entry, got %q (%v)", hosts, err)
	}
	foundMount := false
	for _, m := range inspect.Mounts {
		if m.Destination == "/mnt/test" && !m.RW {
			foundMount = true
		}
	}
	if !foundMount {
		t.Fatalf("expected read-only bind mount, got %+v", inspect.Mounts)
	}

	status, err := c.Status(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if status != models.StatusRunning {
		t.Fatalf("status = %q, want running", status)
	}
	ip, err := c.IPAddress(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ip, "172.28.") {
		t.Fatalf("expected an address on vivarium-net, got %q", ip)
	}

	if err := c.Halt(ctx, id, 5); err != nil {
		t.Fatalf("halt: %v", err)
	}
	status, _ = c.Status(ctx, id)
	if status != models.StatusHalted {
		t.Fatalf("status after halt = %q, want halted", status)
	}
}

// TestIntegrationLiveHostsEnvAndConnect verifies that mock hosts and provider
// env can change without recreating the container: a container-local file
// survives, the new host resolves, and the env is present in a new exec.
func TestIntegrationLiveHostsEnvAndConnect(t *testing.T) {
	if os.Getenv("VIVARIUM_INTEGRATION") == "" {
		t.Skip("set VIVARIUM_INTEGRATION=1 to run Docker integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	c := NewController(DefaultSocket)
	if err := c.Ping(ctx); err != nil {
		t.Skipf("docker daemon unavailable: %v", err)
	}
	if _, err := c.EnsureNetwork(ctx); err != nil {
		t.Fatalf("ensure network: %v", err)
	}

	name := "vivarium-it-live-" + strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "")
	id, err := c.CreateAndStart(ctx, ContainerSpec{
		Name:  name,
		Image: "ubuntu:24.04",
		Cmd:   []string{"sleep", "60"},
	})
	if err != nil {
		t.Fatalf("create and start: %v", err)
	}
	defer func() { _ = c.Remove(ctx, id) }()

	if code, out, err := c.Client().ExecStatus(ctx, id, []string{"sh", "-c", "echo keep > /tmp/keep"}); err != nil || code != 0 {
		t.Fatalf("write file: %v (exit %d, %q)", err, code, out)
	}

	if err := c.SyncHosts(ctx, id, []string{"api.example.com"}, "127.0.0.1"); err != nil {
		t.Fatalf("sync hosts: %v", err)
	}
	hosts, err := c.Client().Exec(ctx, id, []string{"grep", "api.example.com", "/etc/hosts"})
	if err != nil || !strings.Contains(hosts, "127.0.0.1") {
		t.Fatalf("hosts = %q, %v", hosts, err)
	}

	// The container must not have been recreated: the local file survives.
	keep, err := c.Client().Exec(ctx, id, []string{"cat", "/tmp/keep"})
	if err != nil || strings.TrimSpace(keep) != "keep" {
		t.Fatalf("file after host change = %q, %v", keep, err)
	}

	// Provider env is injected into a new exec session.
	stream, _, err := c.Connect(ctx, id, 100, 30, map[string]string{"DEEPSEEK_API_KEY": "viv-tok-test"})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer stream.Close()
	if _, err := io.WriteString(stream, "echo READY_$DEEPSEEK_API_KEY\n"); err != nil {
		t.Fatalf("write to session: %v", err)
	}
	keyRe := regexp.MustCompile(`READY_([A-Za-z0-9_-]+)`)
	if got := strings.TrimSpace(readMatch(t, stream, keyRe)); got != "viv-tok-test" {
		t.Fatalf("injected env = %q, want viv-tok-test", got)
	}
}

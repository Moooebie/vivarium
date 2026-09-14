//go:build integration

package docker

import (
	"context"
	"os"
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
	for _, h := range inspect.HostConfig.ExtraHosts {
		if h == "api.openai.com:172.28.0.1" {
			foundHost = true
		}
	}
	if !foundHost {
		t.Fatalf("expected --add-host entry, got %+v", inspect.HostConfig.ExtraHosts)
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

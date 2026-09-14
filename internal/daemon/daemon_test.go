package daemon

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"vivarium/internal/api"
	"vivarium/internal/auth"
	"vivarium/internal/docker"
	"vivarium/internal/paths"
	"vivarium/internal/store"
)

func newTestDaemon(t *testing.T, dir string) *Daemon {
	t.Helper()
	st := store.New(filepath.Join(dir, "data"))
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	am := auth.NewManager(st, filepath.Join(dir, "data", "vault.enc"))
	dc := docker.NewController("")
	env := paths.Env{XDGDataHome: dir, UID: 1000}
	srv := api.NewServer(st, am, dc, env)
	return New(Options{
		SocketPath: filepath.Join(dir, "vivarium.sock"),
		LockPath:   filepath.Join(dir, "daemon.lock"),
		PIDPath:    filepath.Join(dir, "daemon.pid"),
		Server:     srv,
	})
}

func TestDaemonServesAndLocks(t *testing.T) {
	dir := t.TempDir()
	d := newTestDaemon(t, dir)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	socket := filepath.Join(dir, "vivarium.sock")
	waitForSocket(t, socket)

	pidPath := filepath.Join(dir, "daemon.pid")
	data, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatalf("pid file: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != strconv.Itoa(os.Getpid()) {
		t.Fatalf("pid file = %q, want %d", got, os.Getpid())
	}

	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}}
	resp, err := client.Get("http://docker/api/v1/auth/status")
	if err != nil {
		t.Fatalf("GET status: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status code = %d", resp.StatusCode)
	}

	// A second daemon on the same lock must refuse to start.
	d2 := newTestDaemon(t, dir)
	if err := d2.Run(context.Background()); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("expected ErrAlreadyRunning, got %v", err)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("daemon returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not shut down")
	}
	if _, err := os.Stat(socket); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket not cleaned up: %v", err)
	}
	if _, err := os.Stat(pidPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pid file not cleaned up: %v", err)
	}
}

func waitForSocket(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("socket %s never appeared", path)
}

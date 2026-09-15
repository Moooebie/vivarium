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

var pidPattern = regexp.MustCompile(`PID=([0-9]+)`)

// TestIntegrationConnectIndependentSessions verifies that each Connect call
// yields an independent shell (distinct PID) and that closing one session does
// not affect the other or the container. Enable with VIVARIUM_INTEGRATION=1.
func TestIntegrationConnectIndependentSessions(t *testing.T) {
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

	name := "vivarium-it-connect-" + strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "")
	id, err := c.CreateAndStart(ctx, ContainerSpec{
		Name:  name,
		Image: "ubuntu:24.04",
		Cmd:   []string{"sleep", "60"},
	})
	if err != nil {
		t.Fatalf("create and start: %v", err)
	}
	defer func() { _ = c.Remove(ctx, id) }()

	s1, _, err := c.Connect(ctx, id, 100, 30, nil)
	if err != nil {
		t.Fatalf("connect 1: %v", err)
	}
	defer s1.Close()
	pid1 := shellPID(t, s1)

	s2, _, err := c.Connect(ctx, id, 100, 30, nil)
	if err != nil {
		t.Fatalf("connect 2: %v", err)
	}
	defer s2.Close()
	pid2 := shellPID(t, s2)

	if pid1 == pid2 {
		t.Fatalf("expected independent shells, both have pid %s", pid1)
	}

	// Closing the first session must leave the second (and the container) alive.
	if err := s1.Close(); err != nil {
		t.Fatalf("close session 1: %v", err)
	}
	pid2b := shellPID(t, s2)
	if pid2b != pid2 {
		t.Fatalf("session 2 pid changed after closing session 1: %s -> %s", pid2, pid2b)
	}
	status, err := c.Status(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if status != models.StatusRunning {
		t.Fatalf("container status = %q, want running", status)
	}
}

// shellPID runs a marker command in the session and extracts the shell's PID
// from its output. The echoed command contains "PID=%d" while the result
// contains the resolved numeric PID, so the regex matches only the result.
func shellPID(t *testing.T, s io.ReadWriteCloser) string {
	t.Helper()
	if _, err := io.WriteString(s, "printf '\\nPID=%d\\n' $$\n"); err != nil {
		t.Fatalf("write to session: %v", err)
	}
	return readMatch(t, s, pidPattern)
}

// readMatch reads from r until re matches, returning the first capture group.
func readMatch(t *testing.T, r io.Reader, re *regexp.Regexp) string {
	t.Helper()
	type result struct {
		value string
		ok    bool
	}
	ch := make(chan result, 1)
	go func() {
		buf := make([]byte, 4096)
		var sb strings.Builder
		for {
			n, err := r.Read(buf)
			if n > 0 {
				sb.Write(buf[:n])
				if m := re.FindStringSubmatch(sb.String()); m != nil {
					ch <- result{value: m[1], ok: true}
					return
				}
			}
			if err != nil {
				ch <- result{}
				return
			}
		}
	}()
	select {
	case got := <-ch:
		if !got.ok {
			t.Fatal("session closed before producing a match")
		}
		return got.value
	case <-time.After(20 * time.Second):
		t.Fatal("timed out waiting for session output")
		return ""
	}
}

// TestIntegrationConnectInitialSize verifies the guest PTY is sized to the
// requested dimensions at session start (resize must happen after start).
func TestIntegrationConnectInitialSize(t *testing.T) {
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

	name := "vivarium-it-size-" + strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "")
	id, err := c.CreateAndStart(ctx, ContainerSpec{
		Name:  name,
		Image: "ubuntu:24.04",
		Cmd:   []string{"sleep", "60"},
	})
	if err != nil {
		t.Fatalf("create and start: %v", err)
	}
	defer func() { _ = c.Remove(ctx, id) }()

	stream, _, err := c.Connect(ctx, id, 100, 30, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer stream.Close()
	if _, err := io.WriteString(stream, "echo SIZE_$(stty size)\n"); err != nil {
		t.Fatalf("write to session: %v", err)
	}
	sizeRe := regexp.MustCompile(`SIZE_([0-9]+ [0-9]+)`)
	if got := strings.TrimSpace(readMatch(t, stream, sizeRe)); got != "30 100" {
		t.Fatalf("stty size = %q, want %q", got, "30 100")
	}
}

package paths

import (
	"path/filepath"
	"testing"
)

func TestDataDirUsesXDG(t *testing.T) {
	e := Env{XDGDataHome: "/xdg", Home: "/home/u", UID: 1000}
	if got, want := e.DataDir(), "/xdg/vivarium"; got != want {
		t.Fatalf("DataDir = %q, want %q", got, want)
	}
}

func TestDataDirFallsBackToHome(t *testing.T) {
	e := Env{Home: "/home/u", UID: 1000}
	want := filepath.Join("/home/u", ".local", "share", "vivarium")
	if got := e.DataDir(); got != want {
		t.Fatalf("DataDir = %q, want %q", got, want)
	}
}

func TestSocketPath(t *testing.T) {
	e := Env{UID: 1000, RuntimeDir: "/run/user/1000"}
	if got, want := e.SocketPath(), "/run/user/1000/vivarium/vivarium.sock"; got != want {
		t.Fatalf("SocketPath = %q, want %q", got, want)
	}
	if got, want := e.FallbackSocketPath(), "/tmp/vivarium-1000.sock"; got != want {
		t.Fatalf("FallbackSocketPath = %q, want %q", got, want)
	}
}

func TestStateDirUsesXDG(t *testing.T) {
	e := Env{XDGStateHome: "/state", Home: "/home/u"}
	if got, want := e.StateDir(), "/state/vivarium"; got != want {
		t.Fatalf("StateDir = %q, want %q", got, want)
	}
	if got, want := e.DaemonLogPath(), "/state/vivarium/daemon.log"; got != want {
		t.Fatalf("DaemonLogPath = %q, want %q", got, want)
	}
	if got, want := e.PIDFile(), "/state/vivarium/daemon.pid"; got != want {
		t.Fatalf("PIDFile = %q, want %q", got, want)
	}
}

func TestStateDirFallsBackToHome(t *testing.T) {
	e := Env{Home: "/home/u"}
	want := filepath.Join("/home/u", ".local", "state", "vivarium")
	if got := e.StateDir(); got != want {
		t.Fatalf("StateDir = %q, want %q", got, want)
	}
}

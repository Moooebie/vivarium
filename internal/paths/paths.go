// Package paths resolves the on-disk locations used by Vivarium.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// Env carries the environment inputs used for path resolution. It is a struct
// so tests can exercise resolution without mutating process globals.
type Env struct {
	XDGDataHome  string
	XDGStateHome string
	Home         string
	UID          int
	// RuntimeDir is $XDG_RUNTIME_DIR when set.
	RuntimeDir string
}

// FromOS builds an Env from the current process environment.
func FromOS() Env {
	home, _ := os.UserHomeDir()
	return Env{
		XDGDataHome:  os.Getenv("XDG_DATA_HOME"),
		XDGStateHome: os.Getenv("XDG_STATE_HOME"),
		Home:         home,
		UID:          os.Getuid(),
		RuntimeDir:   os.Getenv("XDG_RUNTIME_DIR"),
	}
}

// DataDir returns the metadata directory (e.g. ~/.local/share/vivarium).
func (e Env) DataDir() string {
	if e.XDGDataHome != "" {
		return filepath.Join(e.XDGDataHome, "vivarium")
	}
	return filepath.Join(e.Home, ".local", "share", "vivarium")
}

// CertsDir returns the directory holding the generated CA material.
func (e Env) CertsDir() string { return filepath.Join(e.DataDir(), "certs") }

// VaultFile returns the path to the encrypted vault.
func (e Env) VaultFile() string { return filepath.Join(e.DataDir(), "vault.enc") }

// ConfigFile returns the path to config.json.
func (e Env) ConfigFile() string { return filepath.Join(e.DataDir(), "config.json") }

// StateDir returns the state directory (e.g. ~/.local/state/vivarium).
func (e Env) StateDir() string {
	if e.XDGStateHome != "" {
		return filepath.Join(e.XDGStateHome, "vivarium")
	}
	return filepath.Join(e.Home, ".local", "state", "vivarium")
}

// DaemonLogPath returns the log file used by a detached backend daemon.
func (e Env) DaemonLogPath() string { return filepath.Join(e.StateDir(), "daemon.log") }

// PIDFile returns the PID file written by a detached backend daemon.
func (e Env) PIDFile() string { return filepath.Join(e.StateDir(), "daemon.pid") }

// SocketPath returns the preferred Unix domain socket path, falling back to a
// per-user /tmp path when no runtime directory is available.
func (e Env) SocketPath() string {
	if e.RuntimeDir != "" {
		return filepath.Join(e.RuntimeDir, "vivarium", "vivarium.sock")
	}
	return filepath.Join("/run/user", strconv.Itoa(e.UID), "vivarium", "vivarium.sock")
}

// FallbackSocketPath returns the /tmp fallback socket path.
func (e Env) FallbackSocketPath() string {
	return fmt.Sprintf("/tmp/vivarium-%d.sock", e.UID)
}

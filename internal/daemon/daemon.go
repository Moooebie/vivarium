// Package daemon owns the Unix domain socket listener, the single-instance
// lock, and the backend lifecycle.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/unix"

	"vivarium/internal/api"
	"vivarium/internal/proxy"
)

// ErrAlreadyRunning indicates another daemon holds the instance lock.
var ErrAlreadyRunning = errors.New("vivarium daemon is already running")

// Options configures a Daemon.
type Options struct {
	// SocketPath is the Unix socket to listen on.
	SocketPath string
	// LockPath is the single-instance lock file.
	LockPath string
	// PIDPath, when set, receives the daemon PID for `vivarium --stop`.
	PIDPath string
	// Server is the configured API server.
	Server *api.Server
	// Bridge is the optional host-bridge proxy started alongside the API.
	Bridge proxy.Bridge
}

// Daemon runs the backend until its context is cancelled.
type Daemon struct {
	opts     Options
	lock     *os.File
	listener net.Listener
}

// New constructs a Daemon.
func New(opts Options) *Daemon { return &Daemon{opts: opts} }

// Run acquires the instance lock, serves the API, and blocks until ctx is
// cancelled.
func (d *Daemon) Run(ctx context.Context) error {
	if err := d.acquireLock(); err != nil {
		return err
	}
	defer d.releaseLock()

	d.writePID()
	defer d.removePID()

	ln, err := d.listen()
	if err != nil {
		return err
	}
	d.listener = ln
	defer d.cleanupSocket()

	if d.opts.Bridge != nil {
		if err := d.opts.Bridge.Start(ctx); err != nil {
			return fmt.Errorf("start host bridge: %w", err)
		}
		defer func() { _ = d.opts.Bridge.Close() }()
	}

	return d.opts.Server.Serve(ctx, ln)
}

func (d *Daemon) acquireLock() error {
	if d.opts.LockPath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(d.opts.LockPath), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(d.opts.LockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return ErrAlreadyRunning
	}
	d.lock = f
	return nil
}

func (d *Daemon) releaseLock() {
	if d.lock != nil {
		_ = unix.Flock(int(d.lock.Fd()), unix.LOCK_UN)
		_ = d.lock.Close()
		d.lock = nil
	}
}

// writePID records the daemon PID for `vivarium --stop`. It is best effort:
// failure to write the file does not prevent the daemon from running.
func (d *Daemon) writePID() {
	if d.opts.PIDPath == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(d.opts.PIDPath), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(d.opts.PIDPath, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600)
}

func (d *Daemon) removePID() {
	if d.opts.PIDPath != "" {
		_ = os.Remove(d.opts.PIDPath)
	}
}

func (d *Daemon) listen() (net.Listener, error) {
	dir := filepath.Dir(d.opts.SocketPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create socket dir: %w", err)
	}
	// We hold the instance lock, so any existing socket is stale.
	if _, err := os.Stat(d.opts.SocketPath); err == nil {
		_ = os.Remove(d.opts.SocketPath)
	}
	ln, err := net.Listen("unix", d.opts.SocketPath)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", d.opts.SocketPath, err)
	}
	if err := os.Chmod(d.opts.SocketPath, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}

func (d *Daemon) cleanupSocket() {
	if d.listener != nil {
		_ = d.listener.Close()
	}
	_ = os.Remove(d.opts.SocketPath)
}

// Close stops the daemon listener.
func (d *Daemon) Close() error {
	if d.listener != nil {
		return d.listener.Close()
	}
	return nil
}

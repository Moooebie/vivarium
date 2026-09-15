// Command vivarium is the single binary that runs the backend daemon and the
// interactive TUI frontend.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"vivarium/internal/api"
	"vivarium/internal/auth"
	"vivarium/internal/client"
	"vivarium/internal/daemon"
	"vivarium/internal/docker"
	"vivarium/internal/keyring"
	"vivarium/internal/paths"
	"vivarium/internal/proxy"
	"vivarium/internal/store"
	"vivarium/internal/tui"
	"vivarium/internal/version"
)

type options struct {
	socket        string
	reset         bool
	assumeYes     bool
	proxyPort     int
	proxyHTTPPort int
	noProxy       bool
	daemon        bool
	stop          bool
	showVersion   bool
}

func main() {
	var opts options
	flag.StringVar(&opts.socket, "socket", "", "override the backend Unix socket path")
	flag.BoolVar(&opts.reset, "reset-credentials", false, "delete stored API keys and reset encryption")
	flag.BoolVar(&opts.reset, "reset-vault", false, "alias for --reset-credentials")
	flag.BoolVar(&opts.assumeYes, "yes", false, "skip confirmation prompts")
	flag.IntVar(&opts.proxyPort, "proxy-port", 8443, "host bridge TLS port on the vivarium-net gateway")
	flag.IntVar(&opts.proxyHTTPPort, "proxy-http-port", 8080, "host bridge plain HTTP port for http:// mock URLs")
	flag.BoolVar(&opts.noProxy, "no-proxy", false, "disable the host bridge proxy")
	flag.BoolVar(&opts.daemon, "daemon", false, "run the backend in the foreground without the TUI")
	flag.BoolVar(&opts.stop, "stop", false, "stop a running backend daemon and exit")
	flag.BoolVar(&opts.showVersion, "version", false, "print the version and exit")
	flag.Parse()

	env := paths.FromOS()
	if err := run(env, opts); err != nil {
		fmt.Fprintln(os.Stderr, "vivarium:", err)
		os.Exit(1)
	}
}

func run(env paths.Env, opts options) error {
	if opts.showVersion {
		fmt.Println(version.Version)
		return nil
	}

	st := store.New(env.DataDir())
	if err := st.Init(); err != nil {
		return err
	}
	am := auth.NewManager(st, env.VaultFile())
	defer am.Close()

	if opts.reset {
		return runReset(am, opts.assumeYes)
	}

	socket := env.SocketPath()
	if opts.socket != "" {
		socket = opts.socket
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if opts.stop {
		return runStop(env, socket)
	}
	if opts.daemon {
		return runDaemon(ctx, env, opts, st, am, socket)
	}
	return runTUI(ctx, env, opts, st, am, socket)
}

// runDaemon runs the backend in the foreground.
func runDaemon(ctx context.Context, env paths.Env, opts options, st *store.Store, am *auth.Manager, socket string) error {
	d := buildDaemon(ctx, env, opts, st, am, socket)
	fmt.Fprintf(os.Stderr, "vivarium backend listening on %s\n", socket)
	return d.Run(ctx)
}

// runTUI connects to a running daemon, or starts a detached one that outlives
// the TUI so several terminals can share it, then runs the TUI.
func runTUI(ctx context.Context, env paths.Env, opts options, st *store.Store, am *auth.Manager, socket string) error {
	cli := client.New(socket)
	if _, err := cli.Ping(ctx); err == nil {
		return tui.Run(ctx, tui.Options{Client: cli, Env: env, Version: version.Version})
	}

	child, err := spawnDaemon(env, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vivarium: detached daemon failed (%v); running in-process\n", err)
		return runTUIInProcess(ctx, env, opts, st, am, socket)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- child.Wait() }()

	if err := waitReady(ctx, cli, errCh); err != nil {
		return err
	}
	return tui.Run(ctx, tui.Options{Client: cli, Env: env, Version: version.Version})
}

// runTUIInProcess starts the backend inside the TUI process and stops it when
// the TUI exits. It is the fallback when a detached daemon cannot be spawned.
func runTUIInProcess(ctx context.Context, env paths.Env, opts options, st *store.Store, am *auth.Manager, socket string) error {
	tuiCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	cli := client.New(socket)
	d := buildDaemon(tuiCtx, env, opts, st, am, socket)
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(tuiCtx) }()

	if err := waitReady(tuiCtx, cli, errCh); err != nil {
		return err
	}
	tuiErr := tui.Run(tuiCtx, tui.Options{Client: cli, Env: env, Version: version.Version})
	cancel() // shuts down the in-process daemon
	select {
	case <-errCh:
	case <-time.After(5 * time.Second):
	}
	return tuiErr
}

// spawnDaemon re-execs this binary as a detached backend daemon. The child runs
// in its own session (so it survives the TUI and terminal signals) and appends
// its output to the daemon log.
func spawnDaemon(env paths.Env, opts options) (*exec.Cmd, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	args := []string{"--daemon"}
	if opts.socket != "" {
		args = append(args, "--socket", opts.socket)
	}
	if opts.noProxy {
		args = append(args, "--no-proxy")
	} else {
		args = append(args, "--proxy-port", strconv.Itoa(opts.proxyPort))
		args = append(args, "--proxy-http-port", strconv.Itoa(opts.proxyHTTPPort))
	}

	logPath := env.DaemonLogPath()
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return nil, err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return nil, err
	}
	// The child holds the only reference to the log descriptor now.
	_ = logFile.Close()
	return cmd, nil
}

// runStop signals a running backend daemon to shut down.
func runStop(env paths.Env, socket string) error {
	cli := client.New(socket)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := cli.Ping(ctx); err != nil {
		return fmt.Errorf("no running daemon on %s", socket)
	}
	data, err := os.ReadFile(env.PIDFile())
	if err != nil {
		return fmt.Errorf("read pid file: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return fmt.Errorf("invalid pid file %s", env.PIDFile())
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("signal daemon: %w", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := cli.Ping(ctx); err != nil {
			fmt.Println("vivarium backend stopped")
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("timed out waiting for the backend to stop")
}

// buildDaemon wires the backend components for a daemon instance.
func buildDaemon(ctx context.Context, env paths.Env, opts options, st *store.Store, am *auth.Manager, socket string) *daemon.Daemon {
	dc := docker.NewController(docker.DefaultSocket)
	registry := proxy.NewRegistry()
	srv := api.NewServer(st, am, dc, env)
	srv.SetRegistry(registry)
	bridge := buildBridge(ctx, env, opts, st, dc, am, registry, srv)
	auditSecrets(st, am)
	reconcileContainers(ctx, srv)
	return daemon.New(daemon.Options{
		SocketPath: socket,
		LockPath:   filepath.Join(env.DataDir(), "daemon.lock"),
		PIDPath:    env.PIDFile(),
		Server:     srv,
		Bridge:     bridge,
	})
}

// auditSecrets logs API keys whose secret is absent from the store, so a lost
// credential is surfaced at startup instead of failing later. A locked vault is
// not an error here.
func auditSecrets(st *store.Store, am *auth.Manager) {
	keys, err := st.ListAPIKeys()
	if err != nil {
		return
	}
	var missing []string
	for _, k := range keys {
		if _, err := am.Secret(k.ID); errors.Is(err, keyring.ErrNotFound) {
			missing = append(missing, k.Name)
		}
	}
	if len(missing) > 0 {
		log.Printf("vivarium: %d API key(s) have no stored secret: %s",
			len(missing), strings.Join(missing, ", "))
	}
}

// reconcileContainers removes managed containers no longer referenced by any
// instance (orphans left by cleared metadata or failed creations). Best effort.
func reconcileContainers(ctx context.Context, srv *api.Server) {
	recCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	n, err := srv.ReconcileContainers(recCtx)
	if err != nil {
		log.Printf("vivarium: container reconcile: %v", err)
		return
	}
	if n > 0 {
		log.Printf("vivarium: removed %d orphaned container(s)", n)
	}
}

// waitReady polls the API until it responds or the daemon fails to start.
func waitReady(ctx context.Context, cli *client.Client, errCh <-chan error) error {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := cli.Ping(ctx); err == nil {
			return nil
		}
		select {
		case err := <-errCh:
			if err == nil || errors.Is(err, daemon.ErrAlreadyRunning) {
				// Another daemon owns the socket; keep waiting for readiness.
				time.Sleep(200 * time.Millisecond)
				continue
			}
			// A detached child may have lost the single-instance race (or
			// crashed); if a daemon is reachable, keep waiting for it.
			if _, perr := cli.Ping(ctx); perr == nil {
				continue
			}
			return fmt.Errorf("backend failed to start: %w", err)
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return errors.New("timed out waiting for the backend to become ready")
}

// buildBridge prepares the host bridge proxy, disabling it cleanly when Docker
// or the network is unavailable.
func buildBridge(ctx context.Context, env paths.Env, opts options, st *store.Store, dc *docker.Controller, am *auth.Manager, registry *proxy.Registry, srv *api.Server) proxy.Bridge {
	if opts.noProxy {
		return proxy.Noop{}
	}
	if _, err := dc.EnsureNetwork(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "vivarium: host bridge disabled: %v\n", err)
		return proxy.Noop{}
	}
	ca, err := proxy.LoadOrCreate(env.CertsDir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "vivarium: host bridge disabled: %v\n", err)
		return proxy.Noop{}
	}
	tlsAddr := net.JoinHostPort(docker.NetworkGateway, strconv.Itoa(opts.proxyPort))
	httpAddr := net.JoinHostPort(docker.NetworkGateway, strconv.Itoa(opts.proxyHTTPPort))
	ps := proxy.NewServer(proxy.Config{
		CA:       ca,
		Registry: registry,
		Secrets:  am,
		TLSAddr:  tlsAddr,
		HTTPAddr: httpAddr,
		Logger:   log.New(os.Stderr, "vivarium proxy: ", 0),
	})
	srv.SetProxyBinding(api.ProxyBinding{
		CACertPEM: ca.CertPEM(),
		TLSPort:   opts.proxyPort,
		HTTPPort:  opts.proxyHTTPPort,
	})
	if err := registry.Reconcile(ctx, st, dc); err != nil {
		fmt.Fprintf(os.Stderr, "vivarium: endpoint reconcile: %v\n", err)
	}
	return ps
}

func runReset(am *auth.Manager, assumeYes bool) error {
	if !assumeYes {
		fmt.Print("WARNING: This will permanently delete all stored API keys, wipe the local vault " +
			"(or keyring records), and reset the master password.\n" +
			"Are you sure you want to proceed? [y/N]: ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		switch strings.TrimSpace(strings.ToLower(line)) {
		case "y", "yes":
		default:
			fmt.Println("Aborted.")
			return nil
		}
	}
	if err := am.Reset(); err != nil {
		return err
	}
	fmt.Println("Credentials reset. Encryption setup will run on the next launch.")
	return nil
}

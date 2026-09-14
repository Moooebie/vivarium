// Command vivarium-guestbridge is a tiny TCP relay injected into agent
// containers. It forwards hijacked provider traffic from loopback to the
// unprivileged Vivarium host bridge on the vivarium-net gateway, preserving
// end-to-end TLS (and therefore SNI/Host) for the proxy to intercept.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

// forwardFlag collects repeated -forward LISTEN=TARGET values.
type forwardFlag []string

func (f *forwardFlag) String() string { return strings.Join(*f, ",") }

func (f *forwardFlag) Set(v string) error {
	*f = append(*f, v)
	return nil
}

func main() {
	var forwards forwardFlag
	var check string
	flag.Var(&forwards, "forward", "LISTEN=TARGET pair (repeatable), e.g. 127.0.0.1:443=172.28.0.1:8443")
	flag.StringVar(&check, "check", "", "dial LISTEN and exit 0 if it accepts, 1 otherwise")
	flag.Parse()

	if check != "" {
		conn, err := net.DialTimeout("tcp", check, time.Second)
		if err != nil {
			os.Exit(1)
		}
		_ = conn.Close()
		return
	}

	if len(forwards) == 0 {
		fmt.Fprintln(os.Stderr, "vivarium-guestbridge: at least one -forward is required")
		os.Exit(2)
	}

	log.SetFlags(0)
	log.SetPrefix("vivarium-guestbridge: ")

	var listeners []net.Listener
	for _, spec := range forwards {
		listen, target, ok := parseForward(spec)
		if !ok {
			log.Fatalf("invalid -forward %q (want LISTEN=TARGET)", spec)
		}
		ln, err := net.Listen("tcp", listen)
		if err != nil {
			log.Fatalf("listen %s: %v", listen, err)
		}
		listeners = append(listeners, ln)
		go serve(ln, target)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	for _, ln := range listeners {
		_ = ln.Close()
	}
}

func parseForward(spec string) (listen, target string, ok bool) {
	i := strings.LastIndex(spec, "=")
	if i <= 0 || i == len(spec)-1 {
		return "", "", false
	}
	return strings.TrimSpace(spec[:i]), strings.TrimSpace(spec[i+1:]), true
}

func serve(ln net.Listener, target string) {
	for {
		client, err := ln.Accept()
		if err != nil {
			return
		}
		go handle(client, target)
	}
}

func handle(client net.Conn, target string) {
	defer client.Close()

	// Wait for the first byte before dialing upstream so readiness probes and
	// port scans do not open backend connections (and produce TLS EOF noise).
	reader := bufio.NewReader(client)
	if _, err := reader.Peek(1); err != nil {
		return
	}

	upstream, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil {
		return
	}
	defer upstream.Close()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(upstream, reader)
		closeWrite(upstream)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(client, upstream)
		closeWrite(client)
	}()
	wg.Wait()
}

// closeWrite half-closes a TCP connection so the peer observes EOF.
func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	}
}

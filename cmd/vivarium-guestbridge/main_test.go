package main

import (
	"io"
	"net"
	"testing"
	"time"
)

func TestParseForward(t *testing.T) {
	listen, target, ok := parseForward("127.0.0.1:443=172.28.0.1:8443")
	if !ok || listen != "127.0.0.1:443" || target != "172.28.0.1:8443" {
		t.Fatalf("parseForward = %q, %q, %v", listen, target, ok)
	}
	if _, _, ok := parseForward("no-equals"); ok {
		t.Fatal("expected invalid forward to fail")
	}
}

func TestRelayEcho(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			conn, err := echo.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()

	relay, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	go serve(relay, echo.Addr().String())

	conn, err := net.Dial("tcp", relay.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))

	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "hello" {
		t.Fatalf("echo = %q", buf)
	}
}

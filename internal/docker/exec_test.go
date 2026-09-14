package docker

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func frame(stream byte, payload string) []byte {
	b := make([]byte, 8+len(payload))
	b[0] = stream
	binary.BigEndian.PutUint32(b[4:8], uint32(len(payload)))
	copy(b[8:], payload)
	return b
}

func TestDemuxStream(t *testing.T) {
	data := append(frame(1, "hello "), frame(2, "oops")...)
	if got := demuxStream(data); got != "hello oops" {
		t.Fatalf("demux = %q", got)
	}
	if got := demuxStream([]byte("plain")); got != "plain" {
		t.Fatalf("plain passthrough = %q", got)
	}
}

func TestCreateExecAndStartExec(t *testing.T) {
	var create execCreateRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/cid/exec"):
			if err := json.NewDecoder(r.Body).Decode(&create); err != nil {
				t.Errorf("decode: %v", err)
			}
			io.WriteString(w, `{"Id":"e9"}`)
		case strings.HasSuffix(r.URL.Path, "/exec/e9/start"):
			var start execStartRequest
			if err := json.NewDecoder(r.Body).Decode(&start); err != nil {
				t.Errorf("decode start: %v", err)
			}
			if start.Detach || !start.Tty {
				t.Errorf("start = %+v", start)
			}
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Error("no hijacker")
				return
			}
			conn, buf, err := hj.Hijack()
			if err != nil {
				t.Errorf("hijack: %v", err)
				return
			}
			defer conn.Close()
			buf.WriteString("HTTP/1.1 101 UPGRADED\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
			buf.Flush()
			io.Copy(conn, conn)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewClientForURL(srv.URL, srv.Client())
	id, err := c.CreateExec(context.Background(), "cid", []string{"bash"}, true, nil)
	if err != nil || id != "e9" {
		t.Fatalf("create = %q, %v", id, err)
	}
	if !create.AttachStdin || !create.AttachStdout || !create.AttachStderr || !create.Tty {
		t.Fatalf("create req = %+v", create)
	}

	stream, err := c.StartExec(context.Background(), id, true)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := stream.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1)
	if _, err := io.ReadFull(stream, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "x" {
		t.Fatalf("echo = %q", buf)
	}
}

func TestResizeExec(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/exec/e1/resize") {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := NewClientForURL(srv.URL, srv.Client())
	if err := c.ResizeExec(context.Background(), "e1", 40, 120); err != nil {
		t.Fatal(err)
	}
	if gotQuery != "h=40&w=120" {
		t.Fatalf("query = %q", gotQuery)
	}
}

func TestExecDetached(t *testing.T) {
	var gotUser string
	var start execStartRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/cid/exec"):
			var req execCreateRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode exec create: %v", err)
			}
			gotUser = req.User
			io.WriteString(w, `{"Id":"e1"}`)
		case strings.HasSuffix(r.URL.Path, "/exec/e1/start"):
			if err := json.NewDecoder(r.Body).Decode(&start); err != nil {
				t.Errorf("decode exec start: %v", err)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewClientForURL(srv.URL, srv.Client())
	if err := c.ExecDetached(context.Background(), "cid", "root", []string{"relay", "-forward", "a=b"}); err != nil {
		t.Fatal(err)
	}
	if gotUser != "root" {
		t.Fatalf("exec user = %q", gotUser)
	}
	if !start.Detach {
		t.Fatal("exec start must be detached")
	}
}

func TestExecStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/cid/exec"):
			io.WriteString(w, `{"Id":"e2"}`)
		case strings.HasSuffix(r.URL.Path, "/exec/e2/start"):
			w.Write(frame(1, "ok"))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/exec/e2/json"):
			io.WriteString(w, `{"ID":"e2","Running":false,"ExitCode":7}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewClientForURL(srv.URL, srv.Client())
	code, out, err := c.ExecStatus(context.Background(), "cid", []string{"relay", "-check", "127.0.0.1:443"})
	if err != nil {
		t.Fatal(err)
	}
	if code != 7 || out != "ok" {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestExec(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/cid/exec"):
			io.WriteString(w, `{"Id":"e1"}`)
		case strings.HasSuffix(r.URL.Path, "/exec/e1/start"):
			io.Copy(io.Discard, r.Body)
			w.Write(frame(1, "root:x:0:0\n"))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewClientForURL(srv.URL, srv.Client())
	out, err := c.Exec(context.Background(), "cid", []string{"cat", "/etc/passwd"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "root:x:0:0\n" {
		t.Fatalf("exec output = %q", out)
	}
}

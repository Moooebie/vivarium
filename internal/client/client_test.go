package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"vivarium/internal/apitypes"
	"vivarium/internal/models"
)

func TestClientMethods(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		switch r.URL.Path {
		case "/api/v1/ping":
			io.WriteString(w, `{"status":"ok","version":"9.9.9"}`)
		case "/api/v1/auth/status":
			io.WriteString(w, `{"status":"unlocked","storage_type":"vault"}`)
		case "/api/v1/instances":
			if r.Method == http.MethodPost {
				var req apitypes.InstanceCreateRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Errorf("decode create: %v", err)
				}
				if req.Name != "dev" {
					t.Errorf("create name = %q", req.Name)
				}
				io.WriteString(w, `{"id":"i1","name":"dev","status":"running","base_image_tag":"img","container_id":"cid"}`)
				return
			}
			io.WriteString(w, `[{"id":"i1","name":"dev","status":"running","base_image_tag":"img","disk_usage_bytes":4096}]`)
		case "/api/v1/instances/i1/exec/e1/resize":
			w.WriteHeader(http.StatusNoContent)
		case "/api/v1/api-keys/k1/secret":
			io.WriteString(w, `{"secret":"sk-real"}`)
		case "/api/v1/api-keys/missing":
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"error":"not found"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"error":"unexpected"}`)
		}
	}))
	defer srv.Close()
	c := NewWithHTTP(srv.URL, srv.Client())
	ctx := context.Background()

	ping, err := c.Ping(ctx)
	if err != nil || ping.Version != "9.9.9" {
		t.Fatalf("ping = %+v, %v", ping, err)
	}
	status, err := c.AuthStatus(ctx)
	if err != nil || status.Status != "unlocked" {
		t.Fatalf("auth status = %+v, %v", status, err)
	}

	inst, err := c.CreateInstance(ctx, apitypes.InstanceCreateRequest{Name: "dev"})
	if err != nil || inst.ID != "i1" || gotMethod != http.MethodPost {
		t.Fatalf("create = %+v, %v (%s)", inst, err, gotMethod)
	}
	views, err := c.ListInstances(ctx)
	if err != nil || len(views) != 1 || views[0].DiskUsageBytes != 4096 {
		t.Fatalf("list = %+v, %v", views, err)
	}

	if err := c.ResizeExec(ctx, "i1", "e1", 120, 40); err != nil {
		t.Fatalf("resize: %v", err)
	}

	secret, err := c.GetAPIKeySecret(ctx, "k1")
	if err != nil || secret != "sk-real" {
		t.Fatalf("secret = %q, %v", secret, err)
	}
	if _, err := c.GetAPIKeySecret(ctx, "missing"); !IsNotFound(err) {
		t.Fatalf("expected not found, got %v", err)
	}
	if _, err := c.GetAPIKeySecret(ctx, "k1"); err != nil || gotPath != "/api/v1/api-keys/k1/secret" {
		t.Fatalf("path = %q", gotPath)
	}
}

func TestClientUpdateInstance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v1/instances/i1" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		var req apitypes.InstanceUpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
		}
		if req.Name != "renamed" || req.Mounts == nil || len(*req.Mounts) != 1 {
			t.Errorf("unexpected req: %+v", req)
		}
		io.WriteString(w, `{"id":"i1","name":"renamed","status":"running","base_image_tag":"img"}`)
	}))
	defer srv.Close()
	c := NewWithHTTP(srv.URL, srv.Client())

	mounts := []models.Mount{{HostPath: "/h", GuestPath: "/g", Mode: models.MountReadWrite}}
	out, err := c.UpdateInstance(context.Background(), "i1", apitypes.InstanceUpdateRequest{Name: "renamed", Mounts: &mounts})
	if err != nil || out.Name != "renamed" {
		t.Fatalf("update = %+v, %v", out, err)
	}
}

func TestClientConnectUpgrade(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("cols"); got != "120" {
			t.Errorf("cols = %q", got)
		}
		if got := r.URL.Query().Get("rows"); got != "40" {
			t.Errorf("rows = %q", got)
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
		buf.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: tcp\r\nConnection: Upgrade\r\n" +
			"X-Vivarium-Exec: e1\r\n\r\n")
		buf.Flush()
		io.Copy(conn, conn)
	}))
	defer srv.Close()

	c := NewWithHTTP(srv.URL, srv.Client())
	rwc, execID, err := c.Connect(context.Background(), "i1", 120, 40)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer rwc.Close()
	if execID != "e1" {
		t.Fatalf("exec id = %q", execID)
	}
	if _, err := rwc.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(rwc, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "hello" {
		t.Fatalf("echo = %q", buf)
	}
	_ = strings.TrimSpace("")
}

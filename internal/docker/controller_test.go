package docker

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"vivarium/internal/models"
)

func TestBuildCreateRequestGPUAndHosts(t *testing.T) {
	c := NewControllerWithClient(NewClient(""), "172.28.0.1")
	spec := ContainerSpec{
		Image:     "vivarium/ubuntu:latest",
		Env:       map[string]string{"B": "2", "A": "1"},
		Mounts:    []models.Mount{{HostPath: "/h", GuestPath: "/g", Mode: models.MountReadOnly}},
		GPUs:      []models.GPU{{DeviceID: "0000:03:00.0", Name: "AMD", RenderNode: "/dev/dri/renderD128"}},
		MockHosts: []string{"api.openai.com"},
	}
	req := c.buildCreateRequest(spec)
	if req.HostConfig.NetworkMode != NetworkName {
		t.Fatalf("network mode = %q", req.HostConfig.NetworkMode)
	}
	if len(req.HostConfig.Mounts) != 1 || !req.HostConfig.Mounts[0].ReadOnly {
		t.Fatalf("unexpected mounts: %+v", req.HostConfig.Mounts)
	}
	if len(req.HostConfig.Devices) != 2 {
		t.Fatalf("expected 2 device mappings, got %+v", req.HostConfig.Devices)
	}
	if req.HostConfig.IpcMode != "host" {
		t.Fatalf("ipc mode = %q", req.HostConfig.IpcMode)
	}
	if got := strings.Join(req.Env, ","); got != "A=1,B=2" {
		t.Fatalf("env = %q", got)
	}
}

func TestEnsureNetworkCreatesWhenMissing(t *testing.T) {
	var created bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/networks/vivarium-net"):
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"message":"network vivarium-net not found"}`)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/networks/create"):
			created = true
			io.WriteString(w, `{"Id":"net123"}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewControllerWithClient(NewClientForURL(srv.URL, srv.Client()), "")
	id, err := c.EnsureNetwork(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if id != "net123" || !created {
		t.Fatalf("id=%q created=%v", id, created)
	}
}

func TestCreateAndStartInjectsResources(t *testing.T) {
	var startCalled bool
	var archivePath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/create"):
			var req containerCreateRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode create: %v", err)
			}
			io.WriteString(w, `{"Id":"cid1"}`)
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/archive"):
			archivePath = r.URL.Query().Get("path")
			io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/start"):
			startCalled = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	src := filepath.Join(dir, "bashrc")
	if err := os.WriteFile(src, []byte("export X=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := NewControllerWithClient(NewClientForURL(srv.URL, srv.Client()), "")
	id, err := c.CreateAndStart(context.Background(), ContainerSpec{
		Name:  "test",
		Image: "vivarium/ubuntu:latest",
		Resources: []models.Resource{
			{HostSourcePath: src, GuestTargetPath: "/root/.bashrc", FileMode: "0644"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != "cid1" {
		t.Fatalf("id = %q", id)
	}
	if !startCalled {
		t.Fatal("container was not started")
	}
	if archivePath != "/" {
		t.Fatalf("archive path = %q, want /", archivePath)
	}
}

func TestCreateAndStartInjectsCA(t *testing.T) {
	var archived []string
	var execRan bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/create"):
			io.WriteString(w, `{"Id":"cid1"}`)
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/archive"):
			archived = append(archived, r.URL.Query().Get("path"))
			io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/containers/cid1/start"):
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/containers/cid1/exec"):
			io.WriteString(w, `{"Id":"e1"}`)
		case strings.HasSuffix(r.URL.Path, "/exec/e1/start"):
			execRan = true
			w.Write(frame(1, "done"))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewControllerWithClient(NewClientForURL(srv.URL, srv.Client()), "")
	if _, err := c.CreateAndStart(context.Background(), ContainerSpec{
		Name:   "test",
		Image:  "ubuntu:24.04",
		CACert: []byte("-----BEGIN CERTIFICATE-----\n"),
	}); err != nil {
		t.Fatal(err)
	}
	if !execRan {
		t.Fatal("update-ca-certificates was not executed")
	}
	if len(archived) != 2 {
		t.Fatalf("expected 2 CA archive writes, got %v", archived)
	}
}

func TestSyncHosts(t *testing.T) {
	var gotCmd []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/cid/exec"):
			var req execCreateRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode exec: %v", err)
			}
			gotCmd = req.Cmd
			io.WriteString(w, `{"Id":"e1"}`)
		case strings.HasSuffix(r.URL.Path, "/exec/e1/start"):
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/exec/e1/json"):
			io.WriteString(w, `{"ExitCode":0}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewControllerWithClient(NewClientForURL(srv.URL, srv.Client()), "")
	if err := c.SyncHosts(context.Background(), "cid", []string{"api.openai.com", "bad host"}, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if len(gotCmd) != 3 || gotCmd[0] != "/bin/sh" || gotCmd[1] != "-c" {
		t.Fatalf("cmd = %v", gotCmd)
	}
	script := gotCmd[2]
	if !strings.Contains(script, "printf '%s %s # vivarium\\n' '127.0.0.1' 'api.openai.com'") {
		t.Fatalf("script missing host entry:\n%s", script)
	}
	if strings.Contains(script, "bad host") {
		t.Fatalf("invalid host was not sanitized:\n%s", script)
	}
}

func TestSanitizeHost(t *testing.T) {
	if got := sanitizeHost("api.openai.com"); got != "api.openai.com" {
		t.Fatalf("sanitizeHost = %q", got)
	}
	if got := sanitizeHost("bad host; rm -rf /"); got != "" {
		t.Fatalf("sanitizeHost = %q", got)
	}
}

func TestStartGuestBridge(t *testing.T) {
	var execUser string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/cid/exec"):
			var req execCreateRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode exec: %v", err)
			}
			if req.User == "root" {
				execUser = "root"
			}
			io.WriteString(w, `{"Id":"e1"}`)
		case strings.HasSuffix(r.URL.Path, "/exec/e1/start"):
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/exec/e1/json"):
			io.WriteString(w, `{"ExitCode":0}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewControllerWithClient(NewClientForURL(srv.URL, srv.Client()), "")
	err := c.StartGuestBridge(context.Background(), "cid", GuestBridgeSpec{
		Path:     "/usr/local/bin/vivarium-guestbridge",
		Forwards: []Forward{{Listen: "127.0.0.1:443", Target: "172.28.0.1:8443"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if execUser != "root" {
		t.Fatalf("guest bridge exec user = %q, want root", execUser)
	}
}

func TestConnect(t *testing.T) {
	var gotCmd []string
	var resizeQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/cid/exec"):
			var req execCreateRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode exec: %v", err)
			}
			gotCmd = req.Cmd
			if !req.Tty || !req.AttachStdin {
				t.Errorf("exec create = %+v", req)
			}
			io.WriteString(w, `{"Id":"e1"}`)
		case strings.HasSuffix(r.URL.Path, "/exec/e1/resize"):
			resizeQuery = r.URL.RawQuery
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/exec/e1/start"):
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

	c := NewControllerWithClient(NewClientForURL(srv.URL, srv.Client()), "")
	stream, execID, err := c.Connect(context.Background(), "cid", 120, 40, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if execID != "e1" {
		t.Fatalf("exec id = %q", execID)
	}
	if len(gotCmd) == 0 || !strings.Contains(strings.Join(gotCmd, " "), "bash") {
		t.Fatalf("shell cmd = %v", gotCmd)
	}
	if resizeQuery != "h=40&w=120" {
		t.Fatalf("resize query = %q", resizeQuery)
	}
}

func TestCreateAndStartInjectsFiles(t *testing.T) {
	var archivePaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/create"):
			io.WriteString(w, `{"Id":"cid1"}`)
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/archive"):
			archivePaths = append(archivePaths, r.URL.Query().Get("path"))
			io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/containers/cid1/start"):
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewControllerWithClient(NewClientForURL(srv.URL, srv.Client()), "")
	if _, err := c.CreateAndStart(context.Background(), ContainerSpec{
		Name:  "test",
		Image: "ubuntu:24.04",
		Files: map[string]InjectedFile{"/etc/opencode.json": {Content: []byte("{}"), Mode: 0o600}},
	}); err != nil {
		t.Fatal(err)
	}
	if len(archivePaths) != 1 || archivePaths[0] != "/" {
		t.Fatalf("archive paths = %v, want [/]", archivePaths)
	}
}

func TestCreateAndStartRejectsRelativeMount(t *testing.T) {
	c := NewControllerWithClient(NewClient(""), "")
	_, err := c.CreateAndStart(context.Background(), ContainerSpec{
		Image:  "x",
		Mounts: []models.Mount{{HostPath: "relative", GuestPath: "/g", Mode: models.MountReadWrite}},
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestGPUGroups(t *testing.T) {
	// Named groups are resolved through the lookup function.
	got := gpuGroups(nil, func(name string) (uint32, bool) {
		if name == "render" {
			return 989, true
		}
		return 0, false
	})
	if len(got) != 1 || got[0] != "989" {
		t.Fatalf("named group resolution = %v, want [989]", got)
	}

	// Device ownership is included and deduplicated.
	f, err := os.CreateTemp(t.TempDir(), "dev")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	fi, err := os.Stat(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	want := strconv.Itoa(int(fi.Sys().(*syscall.Stat_t).Gid))
	got = gpuGroups([]string{f.Name(), f.Name()}, func(string) (uint32, bool) { return 0, false })
	if len(got) != 1 || got[0] != want {
		t.Fatalf("device group = %v, want [%s]", got, want)
	}
}

func TestSanitizeContainerName(t *testing.T) {
	cases := map[string]string{
		"dev-agent sandbox/1": "dev-agent-sandbox-1",
		"---":                 "vivarium-instance",
		"ok.name_1":           "ok.name_1",
		"1":                   "v1",
		"a":                   "va",
	}
	for in, want := range cases {
		if got := SanitizeContainerName(in); got != want {
			t.Fatalf("SanitizeContainerName(%q) = %q, want %q", in, got, want)
		}
	}
}

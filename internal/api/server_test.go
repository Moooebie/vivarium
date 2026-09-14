package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"vivarium/internal/apitypes"
	"vivarium/internal/auth"
	"vivarium/internal/client"
	"vivarium/internal/docker"
	"vivarium/internal/models"
	"vivarium/internal/paths"
	"vivarium/internal/store"
)

type testEnv struct {
	server *Server
	ts     *httptest.Server
	store  *store.Store
	auth   *auth.Manager
}

func fakeDocker(t *testing.T) *httptest.Server {
	return fakeDockerState(t, "running", true)
}

func fakeDockerState(t *testing.T, state string, running bool) *httptest.Server {
	t.Helper()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case p == "/v1.44/_ping":
			io.WriteString(w, "OK")
		case p == "/v1.44/version":
			io.WriteString(w, `{"Version":"29.0.0"}`)
		case r.Method == http.MethodGet && strings.Contains(p, "/images/") && strings.HasSuffix(p, "/json"):
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"message":"no such image"}`)
		case r.Method == http.MethodGet && strings.HasSuffix(p, "/networks/vivarium-net"):
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"message":"not found"}`)
		case r.Method == http.MethodPost && strings.HasSuffix(p, "/networks/create"):
			io.WriteString(w, `{"Id":"net1"}`)
		case r.Method == http.MethodPost && strings.HasSuffix(p, "/containers/create"):
			io.WriteString(w, `{"Id":"cid1"}`)
		case r.Method == http.MethodPost && strings.HasSuffix(p, "/containers/cid1/exec"):
			io.WriteString(w, `{"Id":"e1"}`)
		case r.Method == http.MethodPost && strings.HasSuffix(p, "/exec/e1/start"):
			io.WriteString(w, "ok")
		case r.Method == http.MethodGet && strings.HasSuffix(p, "/exec/e1/json"):
			io.WriteString(w, `{"ID":"e1","Running":false,"ExitCode":0}`)
		case r.Method == http.MethodPost && strings.HasSuffix(p, "/containers/cid1/resize"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && strings.HasSuffix(p, "/containers/cid1/start"),
			r.Method == http.MethodPost && strings.HasSuffix(p, "/containers/cid1/stop"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && strings.Contains(p, "/containers/cid1"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPut && strings.HasSuffix(p, "/archive"):
			io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && strings.HasSuffix(p, "/containers/cid1/json"):
			fmt.Fprintf(w, `{
				"Id":"cid1","State":{"Status":%q,"Running":%t},
				"NetworkSettings":{"Networks":{"vivarium-net":{"IPAddress":"172.28.0.2"}}},
				"SizeRw":4096
			}`, state, running)
		default:
			t.Errorf("unexpected docker request: %s %s", r.Method, p)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	return httptest.NewServer(handler)
}

func newTestEnv(t *testing.T) *testEnv {
	return newTestEnvWithDocker(t, fakeDocker(t))
}

func newTestEnvWithDocker(t *testing.T, dsrv *httptest.Server) *testEnv {
	t.Helper()
	st := store.New(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	am := auth.NewManager(st, filepath.Join(st.Dir(), "vault.enc"))
	if err := am.Setup(models.SecretVault, "pw"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(dsrv.Close)
	dc := docker.NewControllerWithClient(docker.NewClientForURL(dsrv.URL, dsrv.Client()), "")
	env := paths.Env{XDGDataHome: st.Dir(), UID: 1000}
	srv := NewServer(st, am, dc, env)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &testEnv{server: srv, ts: ts, store: st, auth: am}
}

func (e *testEnv) storeSecret(t *testing.T, id, secret string) {
	t.Helper()
	secrets, err := e.auth.Secrets()
	if err != nil {
		t.Fatal(err)
	}
	if err := secrets.StoreSecret(id, secret); err != nil {
		t.Fatal(err)
	}
}

func (e *testEnv) do(t *testing.T, method, path string, body any) (*http.Response, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, e.ts.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, data
}

func TestAuthFlow(t *testing.T) {
	env := newTestEnv(t)

	resp, body := env.do(t, http.MethodGet, "/api/v1/auth/status", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "unlocked") {
		t.Fatalf("status = %d %s", resp.StatusCode, body)
	}

	// Re-running setup once configured is a conflict.
	resp, _ = env.do(t, http.MethodPost, "/api/v1/auth/setup", authSetupRequest{StorageType: models.SecretVault, Password: "x"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 re-running setup, got %d", resp.StatusCode)
	}

	// Locking the vault makes the API report a locked state.
	if err := env.auth.Vault().Lock(); err != nil {
		t.Fatal(err)
	}
	resp, body = env.do(t, http.MethodGet, "/api/v1/auth/status", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "locked") {
		t.Fatalf("locked status = %d %s", resp.StatusCode, body)
	}
	resp, _ = env.do(t, http.MethodPost, "/api/v1/auth/unlock", authUnlockRequest{Password: "wrong"})
	if resp.StatusCode == http.StatusOK {
		t.Fatal("expected unlock failure")
	}
	resp, _ = env.do(t, http.MethodPost, "/api/v1/auth/unlock", authUnlockRequest{Password: "pw"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unlock = %d", resp.StatusCode)
	}
}

func TestAPIKeyCRUDHidesSecret(t *testing.T) {
	env := newTestEnv(t)
	payload := apiKeyPayload{
		APIKey: models.APIKey{
			Name:         "Work OpenAI",
			ProviderType: models.ProviderOpenAI,
			BaseURL:      "https://api.openai.com/v1",
			MockURL:      "https://api.openai.com/v1",
		},
		Secret: "sk-secret",
	}
	resp, body := env.do(t, http.MethodPost, "/api/v1/api-keys", payload)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create key = %d %s", resp.StatusCode, body)
	}
	var created models.APIKey
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.CreatedAt == 0 {
		t.Fatalf("missing server-populated fields: %+v", created)
	}

	resp, body = env.do(t, http.MethodGet, "/api/v1/api-keys", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list keys = %d", resp.StatusCode)
	}
	if strings.Contains(string(body), "sk-secret") {
		t.Fatalf("secret leaked in listing: %s", body)
	}

	secrets, err := env.auth.Secrets()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := secrets.GetSecret(created.ID); err != nil || got != "sk-secret" {
		t.Fatalf("stored secret = %q, %v", got, err)
	}

	resp, _ = env.do(t, http.MethodDelete, "/api/v1/api-keys/"+created.ID, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete key = %d", resp.StatusCode)
	}
}

func TestRecipeDuplicateMockURLRejected(t *testing.T) {
	env := newTestEnv(t)
	key := models.APIKey{ID: "k1", Name: "K", ProviderType: models.ProviderOpenAI,
		BaseURL: "https://api.openai.com/v1", MockURL: "https://api.openai.com/v1", CreatedAt: 1}
	recipe := models.Recipe{Name: "R", BaseImageID: "b1", APIEndpoints: []models.APIKey{key, key}}
	resp, body := env.do(t, http.MethodPost, "/api/v1/recipes", recipe)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d %s", resp.StatusCode, body)
	}
}

func TestRecipeCRUD(t *testing.T) {
	env := newTestEnv(t)
	recipe := models.Recipe{Name: "R", BaseImageID: "b1", EnvVars: map[string]string{"A": "1"}}
	resp, body := env.do(t, http.MethodPost, "/api/v1/recipes", recipe)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create recipe = %d %s", resp.StatusCode, body)
	}
	var created models.Recipe
	json.Unmarshal(body, &created)

	resp, body = env.do(t, http.MethodGet, "/api/v1/recipes/"+created.ID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get recipe = %d", resp.StatusCode)
	}
	created.Name = "Renamed"
	resp, _ = env.do(t, http.MethodPut, "/api/v1/recipes/"+created.ID, created)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update recipe = %d", resp.StatusCode)
	}
	resp, _ = env.do(t, http.MethodDelete, "/api/v1/recipes/"+created.ID, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete recipe = %d", resp.StatusCode)
	}
}

func TestBaseImagePresets(t *testing.T) {
	env := newTestEnv(t)
	resp, body := env.do(t, http.MethodGet, "/api/v1/base-images", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list images = %d", resp.StatusCode)
	}
	var imgs []apitypes.BaseImageView
	json.Unmarshal(body, &imgs)
	if len(imgs) != 2 {
		t.Fatalf("expected 2 presets, got %d", len(imgs))
	}
	resp, _ = env.do(t, http.MethodDelete, "/api/v1/base-images/"+store.StandardOpenCodeImageID, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 deleting preset, got %d", resp.StatusCode)
	}
}

func TestInstanceLifecycle(t *testing.T) {
	env := newTestEnv(t)
	req := instanceCreateRequest{
		Name:         "dev-agent",
		BaseImageTag: "vivarium/ubuntu-24.04-opencode:latest",
	}
	resp, body := env.do(t, http.MethodPost, "/api/v1/instances", req)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create instance = %d %s", resp.StatusCode, body)
	}
	var view instanceView
	if err := json.Unmarshal(body, &view); err != nil {
		t.Fatal(err)
	}
	if view.ContainerID != "cid1" {
		t.Fatalf("container id = %q", view.ContainerID)
	}
	if view.IPAddress != "172.28.0.2" {
		t.Fatalf("ip = %q", view.IPAddress)
	}

	resp, body = env.do(t, http.MethodGet, "/api/v1/instances", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list instances = %d", resp.StatusCode)
	}
	var views []instanceView
	json.Unmarshal(body, &views)
	if len(views) != 1 || views[0].DiskUsageBytes != 4096 {
		t.Fatalf("unexpected instances: %s", body)
	}

	resp, _ = env.do(t, http.MethodPost, "/api/v1/instances/"+view.ID+"/halt", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("halt = %d", resp.StatusCode)
	}
	resp, _ = env.do(t, http.MethodPost, "/api/v1/instances/"+view.ID+"/start", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("start = %d", resp.StatusCode)
	}
	resp, _ = env.do(t, http.MethodDelete, "/api/v1/instances/"+view.ID, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
}

type fakeRegistry struct {
	registered   []string
	unregistered []string
}

func (f *fakeRegistry) Register(i models.Instance) { f.registered = append(f.registered, i.ID) }
func (f *fakeRegistry) Unregister(id string)       { f.unregistered = append(f.unregistered, id) }

func TestInstanceRegistersEndpoints(t *testing.T) {
	env := newTestEnv(t)
	fr := &fakeRegistry{}
	env.server.SetRegistry(fr)
	env.server.SetProxyBinding(ProxyBinding{CACertPEM: []byte("cert"), TLSPort: 8443, HTTPPort: 8080})

	key := models.APIKey{
		ID: "k1", Name: "K", ProviderType: models.ProviderOpenAI,
		BaseURL: "https://api.openai.com/v1", MockURL: "https://api.openai.com/v1", CreatedAt: 1,
	}
	env.storeSecret(t, key.ID, "sk-test")
	recipe := models.Recipe{Name: "r", APIEndpoints: []models.APIKey{key}}
	resp, body := env.do(t, http.MethodPost, "/api/v1/instances", instanceCreateRequest{
		Name: "dev", BaseImageTag: "vivarium/ubuntu:latest", Recipe: &recipe,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d %s", resp.StatusCode, body)
	}
	var view instanceView
	if err := json.Unmarshal(body, &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %+v", view.Endpoints)
	}
	if !strings.HasPrefix(view.Endpoints[0].Token, "viv-tok-") {
		t.Fatalf("unexpected token %q", view.Endpoints[0].Token)
	}
	if view.EnvVars["OPENAI_API_KEY"] == "" {
		t.Fatal("expected dummy API key env var")
	}
	if _, ok := view.EnvVars["OPENAI_BASE_URL"]; ok {
		t.Fatal("base URLs must not be injected")
	}
	if view.EnvVars["SSL_CERT_FILE"] == "" {
		t.Fatal("expected CA trust env var")
	}
	if len(fr.registered) != 1 || fr.registered[0] != view.ID {
		t.Fatalf("registry registrations = %v", fr.registered)
	}

	resp, _ = env.do(t, http.MethodPost, "/api/v1/instances/"+view.ID+"/halt", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("halt = %d", resp.StatusCode)
	}
	if len(fr.unregistered) != 1 || fr.unregistered[0] != view.ID {
		t.Fatalf("registry unregistrations = %v", fr.unregistered)
	}
}

func TestPing(t *testing.T) {
	env := newTestEnv(t)
	resp, body := env.do(t, http.MethodGet, "/api/v1/ping", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"status":"ok"`) {
		t.Fatalf("ping = %d %s", resp.StatusCode, body)
	}
}

func TestCreateAPIKeyFillsProviderDefaults(t *testing.T) {
	env := newTestEnv(t)
	payload := apiKeyPayload{
		APIKey: models.APIKey{Name: "K", ProviderType: models.ProviderOpenAI},
		Secret: "sk-x",
	}
	resp, body := env.do(t, http.MethodPost, "/api/v1/api-keys", payload)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d %s", resp.StatusCode, body)
	}
	var created models.APIKey
	json.Unmarshal(body, &created)
	if created.BaseURL != "https://api.openai.com/v1" || created.MockURL != "https://api.openai.com/v1" {
		t.Fatalf("defaults not applied: %+v", created)
	}
}

func TestAPIKeySecretReveal(t *testing.T) {
	env := newTestEnv(t)
	payload := apiKeyPayload{
		APIKey: models.APIKey{Name: "K", ProviderType: models.ProviderOpenAI,
			BaseURL: "https://api.openai.com/v1", MockURL: "https://api.openai.com/v1"},
		Secret: "sk-reveal-me",
	}
	resp, body := env.do(t, http.MethodPost, "/api/v1/api-keys", payload)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d %s", resp.StatusCode, body)
	}
	var created models.APIKey
	json.Unmarshal(body, &created)

	resp, body = env.do(t, http.MethodGet, "/api/v1/api-keys/"+created.ID+"/secret", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "sk-reveal-me") {
		t.Fatalf("reveal = %d %s", resp.StatusCode, body)
	}
}

func TestUpdateInstanceRenameAndMount(t *testing.T) {
	env := newTestEnv(t)
	resp, body := env.do(t, http.MethodPost, "/api/v1/instances", instanceCreateRequest{
		Name: "dev", BaseImageTag: "vivarium/ubuntu:latest",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d %s", resp.StatusCode, body)
	}
	var view instanceView
	json.Unmarshal(body, &view)

	// Rename only: no recreation.
	resp, body = env.do(t, http.MethodPut, "/api/v1/instances/"+view.ID,
		instanceUpdateRequest{Name: "renamed"})
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"name":"renamed"`) {
		t.Fatalf("rename = %d %s", resp.StatusCode, body)
	}

	// Adding a mount triggers recreation.
	mounts := []models.Mount{{HostPath: "/tmp/host", GuestPath: "/mnt/x", Mode: models.MountReadWrite}}
	resp, body = env.do(t, http.MethodPut, "/api/v1/instances/"+view.ID,
		instanceUpdateRequest{Mounts: &mounts})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mount update = %d %s", resp.StatusCode, body)
	}
	var updated instanceView
	json.Unmarshal(body, &updated)
	if len(updated.Mounts) != 1 || updated.Mounts[0].GuestPath != "/mnt/x" {
		t.Fatalf("mounts = %+v", updated.Mounts)
	}
	if updated.Status != models.StatusRunning {
		t.Fatalf("status = %q", updated.Status)
	}
}

func TestProviderKeyEnv(t *testing.T) {
	eps := []models.InstanceEndpoint{
		{ProviderType: models.ProviderOpenAI, Token: "tok-openai"},
		{ProviderType: models.ProviderCustom, Token: "tok-custom"},
	}
	env := providerKeyEnv(eps)
	if env["OPENAI_API_KEY"] != "tok-openai" {
		t.Fatalf("OPENAI_API_KEY = %q", env["OPENAI_API_KEY"])
	}
	if _, ok := env["CUSTOM_API_KEY"]; ok {
		t.Fatal("custom provider must not get an injected key var")
	}
	if _, ok := env["OPENAI_BASE_URL"]; ok {
		t.Fatal("base URLs must not be injected")
	}
}

func TestRelayForwards(t *testing.T) {
	forwards := relayForwards(8443, 8080)
	if len(forwards) != 2 {
		t.Fatalf("forwards = %+v", forwards)
	}
	if forwards[0].Listen != "127.0.0.1:443" || forwards[0].Target != docker.NetworkGateway+":8443" {
		t.Fatalf("tls forward = %+v", forwards[0])
	}
	if forwards[1].Listen != "127.0.0.1:80" || forwards[1].Target != docker.NetworkGateway+":8080" {
		t.Fatalf("http forward = %+v", forwards[1])
	}
}

func TestInstanceInjectsProviderKeyOnly(t *testing.T) {
	env := newTestEnv(t)
	env.server.SetProxyBinding(ProxyBinding{CACertPEM: []byte("cert"), TLSPort: 8443, HTTPPort: 8080})
	key := models.APIKey{
		ID: "k1", Name: "K", ProviderType: models.ProviderOpenAI,
		BaseURL: "https://api.openai.com/v1", MockURL: "https://api.openai.com/v1", CreatedAt: 1,
	}
	env.storeSecret(t, key.ID, "sk-test")
	recipe := models.Recipe{Name: "r", APIEndpoints: []models.APIKey{key}}
	resp, body := env.do(t, http.MethodPost, "/api/v1/instances", instanceCreateRequest{
		Name: "dev", BaseImageTag: "img", Recipe: &recipe,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d %s", resp.StatusCode, body)
	}
	var view instanceView
	json.Unmarshal(body, &view)
	if view.EnvVars["OPENAI_API_KEY"] == "" {
		t.Fatal("expected the dummy API key env var")
	}
	if _, ok := view.EnvVars["OPENAI_BASE_URL"]; ok {
		t.Fatal("base URL must not be injected")
	}
	if _, ok := view.EnvVars["OPENCODE_CONFIG"]; ok {
		t.Fatal("opencode config must not be injected")
	}
	if view.EnvVars["SSL_CERT_FILE"] == "" {
		t.Fatal("expected CA trust env var")
	}
}

func TestConnectHaltedConflict(t *testing.T) {
	env := newTestEnvWithDocker(t, fakeDockerState(t, "exited", false))
	resp, body := env.do(t, http.MethodPost, "/api/v1/instances", instanceCreateRequest{
		Name: "dev", BaseImageTag: "img",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d %s", resp.StatusCode, body)
	}
	var view instanceView
	json.Unmarshal(body, &view)

	resp, body = env.do(t, http.MethodGet, "/api/v1/instances/"+view.ID+"/connect", nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("connect = %d %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "not running") {
		t.Fatalf("unexpected error: %s", body)
	}
}

// interactiveDocker is a minimal Engine stub for the connect flow: it reports a
// running container and upgrades the exec start request into an echo stream.
func interactiveDocker(t *testing.T) *httptest.Server {
	t.Helper()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case p == "/v1.44/_ping":
			io.WriteString(w, "OK")
		case r.Method == http.MethodGet && strings.HasSuffix(p, "/containers/cid1/json"):
			io.WriteString(w, `{"Id":"cid1","State":{"Status":"running","Running":true}}`)
		case r.Method == http.MethodPost && strings.HasSuffix(p, "/containers/cid1/exec"):
			io.WriteString(w, `{"Id":"e1"}`)
		case r.Method == http.MethodPost && strings.HasSuffix(p, "/exec/e1/resize"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && strings.HasSuffix(p, "/exec/e1/start"):
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Error("docker stub: no hijacker")
				return
			}
			conn, buf, err := hj.Hijack()
			if err != nil {
				t.Errorf("docker stub hijack: %v", err)
				return
			}
			defer conn.Close()
			buf.WriteString("HTTP/1.1 101 UPGRADED\r\n" +
				"Content-Type: application/vnd.docker.raw-stream\r\n" +
				"Connection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
			buf.Flush()
			io.Copy(conn, conn)
		default:
			t.Errorf("unexpected docker request: %s %s", r.Method, p)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	return httptest.NewServer(handler)
}

func TestConnectStreamsAndResize(t *testing.T) {
	env := newTestEnvWithDocker(t, interactiveDocker(t))
	inst := models.Instance{
		ID: "i1", ContainerID: "cid1", Name: "dev",
		Status: models.StatusRunning, BaseImageTag: "img", CreatedAt: 1,
	}
	if err := env.store.PutInstance(inst); err != nil {
		t.Fatal(err)
	}

	cli := client.NewWithHTTP(env.ts.URL, env.ts.Client())
	ctx := context.Background()
	stream, execID, err := cli.Connect(ctx, "i1", 100, 30)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer stream.Close()
	if execID != "e1" {
		t.Fatalf("exec id = %q", execID)
	}
	if _, err := stream.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(stream, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "hello" {
		t.Fatalf("echo = %q", buf)
	}
	if err := cli.ResizeExec(ctx, "i1", execID, 100, 30); err != nil {
		t.Fatalf("resize: %v", err)
	}
}

func TestResizeUnknownExec(t *testing.T) {
	env := newTestEnvWithDocker(t, interactiveDocker(t))
	inst := models.Instance{ID: "i1", ContainerID: "cid1", Name: "dev", Status: models.StatusRunning, BaseImageTag: "img"}
	if err := env.store.PutInstance(inst); err != nil {
		t.Fatal(err)
	}
	cli := client.NewWithHTTP(env.ts.URL, env.ts.Client())
	err := cli.ResizeExec(context.Background(), "i1", "nope", 100, 30)
	if !client.IsNotFound(err) {
		t.Fatalf("expected 404, got %v", err)
	}
}

func TestCreateInstanceFailsWithoutSecret(t *testing.T) {
	env := newTestEnv(t)
	key := models.APIKey{
		ID: "missing", Name: "K", ProviderType: models.ProviderOpenAI,
		BaseURL: "https://api.openai.com/v1", MockURL: "https://api.openai.com/v1", CreatedAt: 1,
	}
	recipe := models.Recipe{Name: "r", APIEndpoints: []models.APIKey{key}}
	resp, body := env.do(t, http.MethodPost, "/api/v1/instances", instanceCreateRequest{
		Name: "dev", BaseImageTag: "img", Recipe: &recipe,
	})
	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("create = %d %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "credential unavailable") {
		t.Fatalf("unexpected error: %s", body)
	}
}

func TestListAPIKeysHasSecretFlag(t *testing.T) {
	env := newTestEnv(t)
	payload := apiKeyPayload{
		APIKey: models.APIKey{Name: "WithSecret", ProviderType: models.ProviderOpenAI,
			BaseURL: "https://api.openai.com/v1", MockURL: "https://api.openai.com/v1"},
		Secret: "sk",
	}
	if resp, body := env.do(t, http.MethodPost, "/api/v1/api-keys", payload); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d %s", resp.StatusCode, body)
	}
	if err := env.store.PutAPIKey(models.APIKey{
		ID: "meta", Name: "NoSecret", ProviderType: models.ProviderOpenAI,
		BaseURL: "https://x/v1", MockURL: "https://x/v1", CreatedAt: 1,
	}); err != nil {
		t.Fatal(err)
	}

	resp, body := env.do(t, http.MethodGet, "/api/v1/api-keys", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list = %d", resp.StatusCode)
	}
	var views []apiKeyView
	if err := json.Unmarshal(body, &views); err != nil {
		t.Fatal(err)
	}
	flags := map[string]bool{}
	for _, v := range views {
		flags[v.Name] = v.HasSecret
	}
	if !flags["WithSecret"] {
		t.Fatal("key with a stored secret should report has_secret=true")
	}
	if flags["NoSecret"] {
		t.Fatal("key without a secret should report has_secret=false")
	}
}

func TestTestConnection(t *testing.T) {
	cases := []struct {
		status int
		ok     bool
		msg    string
	}{
		{200, true, "valid"},
		{401, false, "authentication failed"},
		{404, false, "reachable"},
		{500, false, "provider error"},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/models" {
				t.Errorf("test path = %q, want /v1/models", r.URL.Path)
			}
			if r.Header.Get("Authorization") != "Bearer sk-test" {
				t.Errorf("auth header = %q", r.Header.Get("Authorization"))
			}
			w.WriteHeader(tc.status)
		}))
		key := models.APIKey{ProviderType: models.ProviderOpenAI, BaseURL: srv.URL + "/v1"}
		res := testConnection(context.Background(), key, "sk-test")
		if res.OK != tc.ok || res.Message != tc.msg {
			t.Fatalf("status %d: got ok=%v msg=%q", tc.status, res.OK, res.Message)
		}
		srv.Close()
	}
}

func TestTestAPIKeyPayloadEndpoint(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":[]}`)
	}))
	defer upstream.Close()

	env := newTestEnv(t)
	req := testConnectionRequest{ProviderType: models.ProviderOpenAI, BaseURL: upstream.URL + "/v1", Secret: "sk"}

	// Direct secret.
	resp, body := env.do(t, http.MethodPost, "/api/v1/api-keys/test", req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("test = %d %s", resp.StatusCode, body)
	}
	var res apitypes.TestConnectionResponse
	json.Unmarshal(body, &res)
	if !res.OK {
		t.Fatalf("expected OK, got %+v", res)
	}

	// Stored secret via key_id.
	env.storeSecret(t, "k1", "sk")
	resp, body = env.do(t, http.MethodPost, "/api/v1/api-keys/test", testConnectionRequest{
		ProviderType: models.ProviderOpenAI, BaseURL: upstream.URL + "/v1", KeyID: "k1",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("key_id test = %d %s", resp.StatusCode, body)
	}

	// Neither secret nor key_id.
	resp, _ = env.do(t, http.MethodPost, "/api/v1/api-keys/test", testConnectionRequest{
		ProviderType: models.ProviderOpenAI, BaseURL: upstream.URL + "/v1",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestSystemStatus(t *testing.T) {
	env := newTestEnv(t)
	resp, body := env.do(t, http.MethodGet, "/api/v1/system/status", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("system status = %d", resp.StatusCode)
	}
	var status systemStatusResponse
	json.Unmarshal(body, &status)
	if !status.DockerOK {
		t.Fatalf("docker should be reachable: %s", body)
	}
}

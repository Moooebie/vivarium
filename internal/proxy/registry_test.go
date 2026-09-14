package proxy

import (
	"context"
	"testing"

	"vivarium/internal/models"
)

func testInstance(id, ip string, endpoints ...models.InstanceEndpoint) models.Instance {
	return models.Instance{
		ID: id, ContainerID: "c-" + id, Name: id, Status: models.StatusRunning,
		BaseImageTag: "img", IPAddress: ip, Endpoints: endpoints,
	}
}

func ep(keyID, mockURL, token string) models.InstanceEndpoint {
	return models.InstanceEndpoint{
		KeyID: keyID, ProviderType: models.ProviderOpenAI,
		BaseURL: "https://upstream.example" + "/v1", MockURL: mockURL, Token: token,
	}
}

func TestRegistryMatchLongestPrefix(t *testing.T) {
	r := NewRegistry()
	r.Register(testInstance("i1", "172.28.0.2",
		ep("k1", "https://api.openai.com/v1", "tok-v1"),
		ep("k2", "https://api.openai.com/v1/beta", "tok-beta"),
	))
	got, ok := r.Match("172.28.0.2", "api.openai.com:8443", "/v1/beta/chat")
	if !ok || got.KeyID != "k2" {
		t.Fatalf("expected longest prefix k2, got %+v ok=%v", got, ok)
	}
	got, ok = r.Match("172.28.0.2", "api.openai.com", "/v1/models")
	if !ok || got.KeyID != "k1" {
		t.Fatalf("expected k1, got %+v ok=%v", got, ok)
	}
	if _, ok := r.Match("172.28.0.9", "api.openai.com", "/v1/models"); ok {
		t.Fatal("unknown IP must not match")
	}
	if _, ok := r.Match("172.28.0.2", "evil.example", "/v1/models"); ok {
		t.Fatal("unknown host must not match")
	}
}

func TestRegistryHostFallbackWhenPathDiffers(t *testing.T) {
	r := NewRegistry()
	r.Register(testInstance("i1", "172.28.0.3",
		ep("k1", "https://api.deepseek.com/v1", "tok-1")))

	// Exact path prefix matches.
	if got, ok := r.Match("172.28.0.3", "api.deepseek.com", "/v1/chat/completions"); !ok || got.KeyID != "k1" {
		t.Fatalf("prefix match failed: %+v ok=%v", got, ok)
	}
	// Agent uses a different path (no /v1): still routed by host.
	if got, ok := r.Match("172.28.0.3", "api.deepseek.com", "/chat/completions"); !ok || got.KeyID != "k1" {
		t.Fatalf("host fallback failed: %+v ok=%v", got, ok)
	}
	// Unknown host is still rejected.
	if _, ok := r.Match("172.28.0.3", "evil.example", "/v1/x"); ok {
		t.Fatal("unknown host must not match")
	}
}

func TestRegistryTokenAndUnregister(t *testing.T) {
	r := NewRegistry()
	r.Register(testInstance("i1", "172.28.0.2", ep("k1", "https://api.openai.com/v1", "tok-1")))
	got, ok := r.Match("172.28.0.2", "api.openai.com", "/v1/x")
	if !ok {
		t.Fatal("expected match")
	}
	if !r.ValidateToken(got, "tok-1") {
		t.Fatal("valid token rejected")
	}
	if r.ValidateToken(got, "tok-2") {
		t.Fatal("invalid token accepted")
	}
	r.Unregister("i1")
	if _, ok := r.Match("172.28.0.2", "api.openai.com", "/v1/x"); ok {
		t.Fatal("endpoint survived unregister")
	}
}

type fakeStore struct{ instances []models.Instance }

func (f *fakeStore) ListInstances() ([]models.Instance, error) { return f.instances, nil }
func (f *fakeStore) PutInstance(i models.Instance) error {
	for idx := range f.instances {
		if f.instances[idx].ID == i.ID {
			f.instances[idx] = i
		}
	}
	return nil
}

type fakeResolver map[string]string

func (f fakeResolver) IPAddress(_ context.Context, containerID string) (string, error) {
	return f[containerID], nil
}

func TestRegistryReconcile(t *testing.T) {
	st := &fakeStore{instances: []models.Instance{
		testInstance("i1", "", ep("k1", "https://api.openai.com/v1", "tok-1")),
		{ID: "i2", Status: models.StatusHalted, BaseImageTag: "img"},
	}}
	r := NewRegistry()
	if err := r.Reconcile(context.Background(), st, fakeResolver{"c-i1": "172.28.0.5"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Match("172.28.0.5", "api.openai.com", "/v1/x"); !ok {
		t.Fatal("running instance was not reconciled")
	}
	if st.instances[0].IPAddress != "172.28.0.5" {
		t.Fatalf("resolved IP not persisted: %q", st.instances[0].IPAddress)
	}
}

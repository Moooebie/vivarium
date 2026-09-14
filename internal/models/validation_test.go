package models

import (
	"encoding/json"
	"strings"
	"testing"
)

func validKey() APIKey {
	return APIKey{
		ID:           "k1",
		Name:         "Work OpenAI",
		ProviderType: ProviderOpenAI,
		BaseURL:      "https://api.openai.com/v1",
		MockURL:      "https://api.openai.com/v1",
		RateLimitRPM: 60,
		CreatedAt:    1773440000,
	}
}

func TestAPIKeyValidate(t *testing.T) {
	if err := validKey().Validate(); err != nil {
		t.Fatalf("valid key rejected: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*APIKey)
	}{
		{"missing id", func(k *APIKey) { k.ID = "" }},
		{"bad provider", func(k *APIKey) { k.ProviderType = "gemini" }},
		{"relative base url", func(k *APIKey) { k.BaseURL = "api.openai.com" }},
		{"bad scheme", func(k *APIKey) { k.BaseURL = "ftp://x" }},
		{"missing created_at", func(k *APIKey) { k.CreatedAt = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k := validKey()
			tt.mutate(&k)
			if err := k.Validate(); err == nil {
				t.Fatalf("expected error for %s", tt.name)
			}
		})
	}
}

func TestRecipeValidateDuplicateMockURL(t *testing.T) {
	r := Recipe{
		ID:          "r1",
		Name:        "Standard Python Agent",
		BaseImageID: "b1",
		APIEndpoints: []APIKey{
			validKey(),
			validKey(),
		},
	}
	if err := r.Validate(); err == nil {
		t.Fatal("expected duplicate mock_url error")
	} else if !strings.Contains(err.Error(), "duplicate mock_url") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRecipeValidateOK(t *testing.T) {
	k1 := validKey()
	k2 := validKey()
	k2.ID = "k2"
	k2.MockURL = "https://api.anthropic.com"
	k2.BaseURL = "https://api.anthropic.com"
	k2.ProviderType = ProviderAnthropic
	r := Recipe{
		ID:            "r1",
		Name:          "Standard Python Agent",
		BaseImageID:   "b1",
		APIEndpoints:  []APIKey{k1, k2},
		EnvVars:       map[string]string{"PYTHONUNBUFFERED": "1"},
		Resources:     []Resource{{HostSourcePath: "/home/u/.bashrc", GuestTargetPath: "/root/.bashrc", FileMode: "0644"}},
		DefaultMounts: []Mount{{HostPath: "/home/u/repo", GuestPath: "/mnt/repo", Mode: MountReadWrite}},
		GPUs:          []GPU{{DeviceID: "0000:03:00.0", Name: "AMD", RenderNode: "/dev/dri/renderD128"}},
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("valid recipe rejected: %v", err)
	}
}

func TestResourceValidate(t *testing.T) {
	base := Resource{HostSourcePath: "/a", GuestTargetPath: "/b", FileMode: "0644"}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid resource rejected: %v", err)
	}
	bad := base
	bad.HostSourcePath = "relative"
	if err := bad.Validate(); err == nil {
		t.Fatal("expected relative path error")
	}
	bad = base
	bad.FileMode = "abc"
	if err := bad.Validate(); err == nil {
		t.Fatal("expected octal error")
	}
}

func TestMountValidate(t *testing.T) {
	base := Mount{HostPath: "/a", GuestPath: "/b", Mode: MountReadOnly}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid mount rejected: %v", err)
	}
	bad := base
	bad.Mode = "rwx"
	if err := bad.Validate(); err == nil {
		t.Fatal("expected mode error")
	}
}

func TestInstanceValidateEndpoints(t *testing.T) {
	base := Instance{
		ID: "i1", Name: "dev", Status: StatusRunning, BaseImageTag: "img",
		Endpoints: []InstanceEndpoint{{
			KeyID: "k1", ProviderType: ProviderOpenAI,
			BaseURL: "https://api.openai.com/v1", MockURL: "https://api.openai.com/v1", Token: "viv-tok-1",
		}},
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid instance rejected: %v", err)
	}
	dup := base
	dup.Endpoints = append(dup.Endpoints, dup.Endpoints[0])
	if err := dup.Validate(); err == nil {
		t.Fatal("expected duplicate endpoint error")
	}
	missingToken := base
	missingToken.Endpoints[0].Token = ""
	if err := missingToken.Validate(); err == nil {
		t.Fatal("expected missing token error")
	}
}

func TestProviderDefaults(t *testing.T) {
	if got := ProviderDefaultURL(ProviderOpenAI); got != "https://api.openai.com/v1" {
		t.Fatalf("openai default = %q", got)
	}
	if got := ProviderDefaultURL(ProviderAnthropic); got != "https://api.anthropic.com" {
		t.Fatalf("anthropic default = %q", got)
	}
	if got := ProviderDefaultURL(ProviderDeepSeek); got != "https://api.deepseek.com/v1" {
		t.Fatalf("deepseek default = %q", got)
	}
	if got := ProviderDefaultURL(ProviderOpenRouter); got != "https://openrouter.ai/api/v1" {
		t.Fatalf("openrouter default = %q", got)
	}
	if got := ProviderDefaultURL(ProviderCustom); got != "" {
		t.Fatalf("custom should have no default, got %q", got)
	}

	keyVar, baseVars := ProviderEnvNames(ProviderDeepSeek)
	if keyVar != "DEEPSEEK_API_KEY" || !contains(baseVars, "DEEPSEEK_API_BASE") {
		t.Fatalf("deepseek env names = %q, %v", keyVar, baseVars)
	}
	keyVar, baseVars = ProviderEnvNames(ProviderAnthropic)
	if keyVar != "ANTHROPIC_API_KEY" || !contains(baseVars, "ANTHROPIC_BASE_URL") {
		t.Fatalf("anthropic env names = %q, %v", keyVar, baseVars)
	}
	_, baseVars = ProviderEnvNames(ProviderOpenAI)
	if !contains(baseVars, "OPENAI_BASE_URL") || !contains(baseVars, "OPENAI_API_BASE") {
		t.Fatalf("openai base vars = %v", baseVars)
	}
}

func contains(items []string, want string) bool {
	for _, it := range items {
		if it == want {
			return true
		}
	}
	return false
}

func TestProviderTestURL(t *testing.T) {
	cases := []struct {
		provider ProviderType
		base     string
		want     string
	}{
		{ProviderOpenAI, "https://api.openai.com/v1", "https://api.openai.com/v1/models"},
		{ProviderDeepSeek, "https://api.deepseek.com/v1/", "https://api.deepseek.com/v1/models"},
		{ProviderAnthropic, "https://api.anthropic.com", "https://api.anthropic.com/v1/models"},
		{ProviderAnthropic, "https://api.anthropic.com/v1", "https://api.anthropic.com/v1/models"},
		{ProviderOpenRouter, "https://openrouter.ai/api/v1", "https://openrouter.ai/api/v1/models"},
	}
	for _, tc := range cases {
		if got := ProviderTestURL(tc.provider, tc.base); got != tc.want {
			t.Fatalf("ProviderTestURL(%s, %q) = %q, want %q", tc.provider, tc.base, got, tc.want)
		}
	}
}

func TestJSONRoundTrip(t *testing.T) {
	in := validKey()
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out APIKey
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("round trip mismatch: %+v vs %+v", in, out)
	}
}

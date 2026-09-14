// Package models defines the persisted Vivarium entities described in
// DATA_MODELS.md together with their validation rules.
package models

import (
	"strings"

	"github.com/google/uuid"
)

// SourceType enumerates where a base image comes from.
type SourceType string

const (
	SourceStandard   SourceType = "standard"
	SourceDockerHub  SourceType = "dockerhub"
	SourceDockerfile SourceType = "dockerfile"
)

// ProviderType enumerates credential archetypes.
type ProviderType string

const (
	ProviderOpenAI     ProviderType = "openai"
	ProviderAnthropic  ProviderType = "anthropic"
	ProviderDeepSeek   ProviderType = "deepseek"
	ProviderOpenRouter ProviderType = "openrouter"
	ProviderCustom     ProviderType = "custom"
)

// InstanceStatus enumerates container execution states.
type InstanceStatus string

const (
	StatusRunning  InstanceStatus = "running"
	StatusHalted   InstanceStatus = "halted"
	StatusBuilding InstanceStatus = "building"
	StatusError    InstanceStatus = "error"
)

// MountMode enumerates bind mount permission modes.
type MountMode string

const (
	MountReadOnly  MountMode = "ro"
	MountReadWrite MountMode = "rw"
)

// BaseImage defines a base container runtime (base_images.json).
type BaseImage struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	SourceType       SourceType `json:"source_type"`
	SourcePathOrRepo string     `json:"source_path_or_repo"`
	DefaultUser      string     `json:"default_user"`
	IsInternal       bool       `json:"is_internal"`
}

// APIKey is API key metadata (api_keys.json). The secret itself lives in the
// active SecretStore keyed by ID.
type APIKey struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	ProviderType ProviderType `json:"provider_type"`
	BaseURL      string       `json:"base_url"`
	MockURL      string       `json:"mock_url"`
	RateLimitRPM int          `json:"rate_limit_rpm"`
	CreatedAt    int64        `json:"created_at"`
}

// Resource describes a host file copied into the guest on creation.
type Resource struct {
	HostSourcePath  string `json:"host_source_path"`
	GuestTargetPath string `json:"guest_target_path"`
	FileMode        string `json:"file_mode"`
}

// Mount describes a host directory bind mounted into the guest.
type Mount struct {
	HostPath  string    `json:"host_path"`
	GuestPath string    `json:"guest_path"`
	Mode      MountMode `json:"mode"`
}

// GPU describes a host GPU allocation.
type GPU struct {
	DeviceID   string `json:"device_id"`
	Name       string `json:"name"`
	RenderNode string `json:"render_node"`
}

// Recipe is a reusable blueprint for constructing an agent sandbox.
type Recipe struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	BaseImageID   string            `json:"base_image_id"`
	APIEndpoints  []APIKey          `json:"api_endpoints"`
	EnvVars       map[string]string `json:"env_vars"`
	Resources     []Resource        `json:"resources"`
	DefaultMounts []Mount           `json:"default_mounts"`
	GPUs          []GPU             `json:"gpus"`
}

// InstanceEndpoint binds a recipe API endpoint to a running instance. It is an
// extension to DATA_MODELS.md used by the host-bridge proxy. Token is a dummy
// viv-tok-* value; the real secret lives only in the SecretStore.
type InstanceEndpoint struct {
	KeyID        string       `json:"key_id"`
	ProviderType ProviderType `json:"provider_type"`
	BaseURL      string       `json:"base_url"`
	MockURL      string       `json:"mock_url"`
	Token        string       `json:"token"`
	RateLimitRPM int          `json:"rate_limit_rpm"`
}

// Instance represents a managed container (instances.json).
type Instance struct {
	ID           string             `json:"id"`
	ContainerID  string             `json:"container_id"`
	Name         string             `json:"name"`
	RecipeID     *string            `json:"recipe_id"`
	Status       InstanceStatus     `json:"status"`
	CreatedAt    int64              `json:"created_at"`
	LastRunAt    int64              `json:"last_run_at"`
	BaseImageTag string             `json:"base_image_tag"`
	Mounts       []Mount            `json:"mounts"`
	GPUs         []GPU              `json:"gpus"`
	IPAddress    string             `json:"ip_address"`
	EnvVars      map[string]string  `json:"env_vars"`
	Endpoints    []InstanceEndpoint `json:"endpoints"`
	// Resources and User are retained so a recreated container is faithful.
	Resources []Resource `json:"resources,omitempty"`
	User      string     `json:"user,omitempty"`
}

// SecretStorageType selects the credential backend.
type SecretStorageType string

const (
	SecretLibsecret SecretStorageType = "libsecret"
	SecretVault     SecretStorageType = "vault"
)

// Config is persisted in config.json.
type Config struct {
	SecretStorageType SecretStorageType `json:"secret_storage_type"`
	// Configured records whether encryption setup has completed. It
	// distinguishes a fresh install from one that selected a backend.
	Configured bool `json:"configured"`
}

// NewID returns a new random UUIDv4 string.
func NewID() string {
	return uuid.NewString()
}

// ProviderDefaultURL returns the conventional base/mock URL for a provider.
// It returns "" for custom providers, which have no default.
func ProviderDefaultURL(p ProviderType) string {
	switch p {
	case ProviderOpenAI:
		return "https://api.openai.com/v1"
	case ProviderAnthropic:
		return "https://api.anthropic.com"
	case ProviderDeepSeek:
		return "https://api.deepseek.com/v1"
	case ProviderOpenRouter:
		return "https://openrouter.ai/api/v1"
	default:
		return ""
	}
}

// ProviderTestURL returns the models endpoint used to validate a credential.
func ProviderTestURL(p ProviderType, baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if p == ProviderAnthropic && !strings.HasSuffix(base, "/v1") {
		return base + "/v1/models"
	}
	return base + "/models"
}

// ProviderEnvNames returns the API key and base URL environment variable names
// for a provider. Several tools use different base URL names (for example
// OpenCode reads OPENAI_BASE_URL while older SDKs read OPENAI_API_BASE), so all
// known aliases are returned.
func ProviderEnvNames(p ProviderType) (apiKeyVar string, baseURLVars []string) {
	switch p {
	case ProviderOpenAI:
		return "OPENAI_API_KEY", []string{"OPENAI_BASE_URL", "OPENAI_API_BASE"}
	case ProviderAnthropic:
		return "ANTHROPIC_API_KEY", []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_API_BASE"}
	case ProviderDeepSeek:
		return "DEEPSEEK_API_KEY", []string{"DEEPSEEK_BASE_URL", "DEEPSEEK_API_BASE"}
	case ProviderOpenRouter:
		return "OPENROUTER_API_KEY", []string{"OPENROUTER_BASE_URL", "OPENROUTER_API_BASE"}
	case ProviderCustom:
		return "CUSTOM_API_KEY", []string{"CUSTOM_BASE_URL", "CUSTOM_API_BASE"}
	default:
		return "", nil
	}
}

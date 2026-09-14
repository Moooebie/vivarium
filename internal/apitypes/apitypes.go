// Package apitypes defines the JSON wire types shared by the backend API and
// the frontend client.
package apitypes

import "vivarium/internal/models"

// AuthStatusResponse is returned by GET /auth/status.
type AuthStatusResponse struct {
	Status      string `json:"status"`
	StorageType string `json:"storage_type,omitempty"`
}

// AuthSetupRequest is the body of POST /auth/setup.
type AuthSetupRequest struct {
	StorageType models.SecretStorageType `json:"storage_type"`
	Password    string                   `json:"password"`
}

// AuthUnlockRequest is the body of POST /auth/unlock.
type AuthUnlockRequest struct {
	Password string `json:"password"`
}

// APIKeyPayload carries API key metadata plus its secret.
type APIKeyPayload struct {
	models.APIKey
	Secret string `json:"secret"`
}

// Secret-state values reported for an API key. They distinguish a missing
// secret from a locked or temporarily unavailable store.
const (
	SecretOK      = "ok"
	SecretLocked  = "locked"
	SecretMissing = "missing"
	SecretError   = "error"
)

// APIKeyView augments API key metadata with the state of its stored secret.
type APIKeyView struct {
	models.APIKey
	HasSecret   bool   `json:"has_secret"`
	SecretState string `json:"secret_state"`
	SecretError string `json:"secret_error,omitempty"`
}

// SecretResponse is returned by GET /api-keys/{id}/secret.
type SecretResponse struct {
	Secret string `json:"secret"`
}

// InstanceCreateRequest accepts either a saved recipe reference or an ephemeral
// recipe, plus per-instance overrides.
type InstanceCreateRequest struct {
	Name         string         `json:"name"`
	RecipeID     *string        `json:"recipe_id"`
	Recipe       *models.Recipe `json:"recipe"`
	BaseImageTag string         `json:"base_image_tag"`
	Mounts       []models.Mount `json:"mounts"`
	GPUs         []models.GPU   `json:"gpus"`
}

// InstanceUpdateRequest is the body of PUT /instances/{id}. Only the name and
// the bound API endpoints can change after creation; mounts and GPUs are fixed
// at create time.
type InstanceUpdateRequest struct {
	Name         string    `json:"name"`
	EndpointKeys *[]string `json:"endpoint_keys"`
}

// InstanceView augments a persisted instance with live Docker data.
type InstanceView struct {
	models.Instance
	DiskUsageBytes int64 `json:"disk_usage_bytes"`
}

// ResizeRequest is the body of POST /instances/{id}/exec/{execID}/resize.
type ResizeRequest struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// InjectRequest is the body of POST /instances/{id}/inject.
type InjectRequest struct {
	HostSourcePath  string `json:"host_source_path"`
	GuestTargetPath string `json:"guest_target_path"`
	FileMode        string `json:"file_mode"`
}

// TestConnectionRequest tests a credential without persisting it. When Secret
// is empty and KeyID is set, the stored secret is used.
type TestConnectionRequest struct {
	ProviderType models.ProviderType `json:"provider_type"`
	BaseURL      string              `json:"base_url"`
	Secret       string              `json:"secret"`
	KeyID        string              `json:"key_id,omitempty"`
}

// TestConnectionResponse is returned by the API key test endpoints.
type TestConnectionResponse struct {
	OK         bool   `json:"ok"`
	StatusCode int    `json:"status_code"`
	Message    string `json:"message,omitempty"`
	Error      string `json:"error,omitempty"`
}

// BaseImageView augments a base image with local availability information.
type BaseImageView struct {
	models.BaseImage
	Present   bool  `json:"present"`
	SizeBytes int64 `json:"size_bytes"`
}

// GPUListResponse is returned by GET /system/gpus.
type GPUListResponse struct {
	GPUs []models.GPU `json:"gpus"`
}

// SystemStatusResponse is returned by GET /system/status.
type SystemStatusResponse struct {
	DockerOK          bool   `json:"docker_ok"`
	DockerVersion     string `json:"docker_version,omitempty"`
	KeyringAvailable  bool   `json:"keyring_available"`
	CACertPresent     bool   `json:"ca_cert_present"`
	EncryptionMode    string `json:"encryption_mode,omitempty"`
	KeyringCollection string `json:"keyring_collection,omitempty"`
	SocketPath        string `json:"socket_path"`
	Version           string `json:"version,omitempty"`
}

// PingResponse is returned by GET /ping.
type PingResponse struct {
	Status  string `json:"status"`
	Version string `json:"version,omitempty"`
}

// ErrorResponse is the uniform error envelope.
type ErrorResponse struct {
	Error string `json:"error"`
}

// RebuildRequest optionally selects a single standard preset to rebuild.
type RebuildRequest struct {
	Key string `json:"key"`
}

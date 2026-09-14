package tui

import (
	"context"
	"io"

	"vivarium/internal/apitypes"
	"vivarium/internal/models"
)

// API is the subset of the backend client the TUI uses. It is an interface so
// screens can be tested with a fake.
type API interface {
	Ping(ctx context.Context) (apitypes.PingResponse, error)
	AuthStatus(ctx context.Context) (apitypes.AuthStatusResponse, error)
	AuthSetup(ctx context.Context, storage models.SecretStorageType, password string) (apitypes.AuthStatusResponse, error)
	AuthUnlock(ctx context.Context, password string) (apitypes.AuthStatusResponse, error)

	ListBaseImages(ctx context.Context) ([]apitypes.BaseImageView, error)
	CreateBaseImage(ctx context.Context, img models.BaseImage) (models.BaseImage, error)
	DeleteBaseImage(ctx context.Context, id string) error
	RebuildBaseImages(ctx context.Context, key string, logs io.Writer) error

	ListAPIKeys(ctx context.Context) ([]apitypes.APIKeyView, error)
	CreateAPIKey(ctx context.Context, payload apitypes.APIKeyPayload) (models.APIKey, error)
	UpdateAPIKey(ctx context.Context, id string, payload apitypes.APIKeyPayload) (models.APIKey, error)
	DeleteAPIKey(ctx context.Context, id string) error
	GetAPIKeySecret(ctx context.Context, id string) (string, error)
	TestAPIKey(ctx context.Context, id string) (apitypes.TestConnectionResponse, error)
	TestAPIKeyPayload(ctx context.Context, req apitypes.TestConnectionRequest) (apitypes.TestConnectionResponse, error)

	ListRecipes(ctx context.Context) ([]models.Recipe, error)
	GetRecipe(ctx context.Context, id string) (models.Recipe, error)
	CreateRecipe(ctx context.Context, r models.Recipe) (models.Recipe, error)
	UpdateRecipe(ctx context.Context, id string, r models.Recipe) (models.Recipe, error)
	DeleteRecipe(ctx context.Context, id string) error

	ListInstances(ctx context.Context) ([]apitypes.InstanceView, error)
	GetInstance(ctx context.Context, id string) (apitypes.InstanceView, error)
	CreateInstance(ctx context.Context, req apitypes.InstanceCreateRequest) (apitypes.InstanceView, error)
	UpdateInstance(ctx context.Context, id string, req apitypes.InstanceUpdateRequest) (apitypes.InstanceView, error)
	StartInstance(ctx context.Context, id string) (apitypes.InstanceView, error)
	HaltInstance(ctx context.Context, id string) (apitypes.InstanceView, error)
	DeleteInstance(ctx context.Context, id string) error
	InjectInstance(ctx context.Context, id string, req apitypes.InjectRequest) error

	ListGPUs(ctx context.Context) ([]models.GPU, error)
	SystemStatus(ctx context.Context) (apitypes.SystemStatusResponse, error)
	Connect(ctx context.Context, id string, cols, rows int) (io.ReadWriteCloser, string, error)
	ResizeExec(ctx context.Context, id, execID string, width, height int) error
}

// Package client is the typed Unix-socket API client used by the TUI.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"vivarium/internal/apitypes"
	"vivarium/internal/models"
)

// APIError is a structured error returned by the Vivarium API.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("api %d: %s", e.StatusCode, e.Message)
}

// IsNotFound reports whether err is an HTTP 404 from the API.
func IsNotFound(err error) bool {
	var apiErr *APIError
	return asAPIError(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

func asAPIError(err error, target **APIError) bool {
	for err != nil {
		if e, ok := err.(*APIError); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// Client talks to the Vivarium API over a Unix socket.
type Client struct {
	http    *http.Client
	baseURL string
}

// New returns a client bound to the API socket at socketPath.
func New(socketPath string) *Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
	}
	return &Client{http: &http.Client{Transport: transport}, baseURL: "http://docker"}
}

// NewWithHTTP returns a client for an arbitrary base URL (tests).
func NewWithHTTP(baseURL string, hc *http.Client) *Client {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{http: hc, baseURL: strings.TrimRight(baseURL, "/")}
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		msg := strings.TrimSpace(readAll(resp.Body))
		var envelope apitypes.ErrorResponse
		if json.Unmarshal([]byte(msg), &envelope) == nil && envelope.Error != "" {
			msg = envelope.Error
		}
		if msg == "" {
			msg = resp.Status
		}
		return &APIError{StatusCode: resp.StatusCode, Message: msg}
	}
	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func readAll(r io.Reader) string {
	data, _ := io.ReadAll(io.LimitReader(r, 8192))
	return string(data)
}

// Ping checks daemon readiness.
func (c *Client) Ping(ctx context.Context) (apitypes.PingResponse, error) {
	var out apitypes.PingResponse
	err := c.do(ctx, http.MethodGet, "/api/v1/ping", nil, &out)
	return out, err
}

// ---- Auth ----

// AuthStatus returns the credential-store status.
func (c *Client) AuthStatus(ctx context.Context) (apitypes.AuthStatusResponse, error) {
	var out apitypes.AuthStatusResponse
	err := c.do(ctx, http.MethodGet, "/api/v1/auth/status", nil, &out)
	return out, err
}

// AuthSetup configures the credential store.
func (c *Client) AuthSetup(ctx context.Context, storage models.SecretStorageType, password string) (apitypes.AuthStatusResponse, error) {
	var out apitypes.AuthStatusResponse
	err := c.do(ctx, http.MethodPost, "/api/v1/auth/setup",
		apitypes.AuthSetupRequest{StorageType: storage, Password: password}, &out)
	return out, err
}

// AuthUnlock unlocks the vault.
func (c *Client) AuthUnlock(ctx context.Context, password string) (apitypes.AuthStatusResponse, error) {
	var out apitypes.AuthStatusResponse
	err := c.do(ctx, http.MethodPost, "/api/v1/auth/unlock",
		apitypes.AuthUnlockRequest{Password: password}, &out)
	return out, err
}

// ---- Base Images ----

// ListBaseImages returns all base images with local availability information.
func (c *Client) ListBaseImages(ctx context.Context) ([]apitypes.BaseImageView, error) {
	var out []apitypes.BaseImageView
	err := c.do(ctx, http.MethodGet, "/api/v1/base-images", nil, &out)
	return out, err
}

// CreateBaseImage registers a custom base image.
func (c *Client) CreateBaseImage(ctx context.Context, img models.BaseImage) (models.BaseImage, error) {
	var out models.BaseImage
	err := c.do(ctx, http.MethodPost, "/api/v1/base-images", img, &out)
	return out, err
}

// DeleteBaseImage removes a custom base image.
func (c *Client) DeleteBaseImage(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/v1/base-images/"+id, nil, nil)
}

// RebuildBaseImages streams build logs for standard presets.
func (c *Client) RebuildBaseImages(ctx context.Context, key string, logs io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/api/v1/base-images/rebuild", strings.NewReader(`{"key":"`+key+`"}`))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return &APIError{StatusCode: resp.StatusCode, Message: strings.TrimSpace(readAll(resp.Body))}
	}
	_, err = io.Copy(logs, resp.Body)
	return err
}

// ---- API Keys ----

// ListAPIKeys returns API key metadata with a secret-presence flag.
func (c *Client) ListAPIKeys(ctx context.Context) ([]apitypes.APIKeyView, error) {
	var out []apitypes.APIKeyView
	err := c.do(ctx, http.MethodGet, "/api/v1/api-keys", nil, &out)
	return out, err
}

// CreateAPIKey stores metadata and a secret.
func (c *Client) CreateAPIKey(ctx context.Context, payload apitypes.APIKeyPayload) (models.APIKey, error) {
	var out models.APIKey
	err := c.do(ctx, http.MethodPost, "/api/v1/api-keys", payload, &out)
	return out, err
}

// UpdateAPIKey updates metadata and optionally the secret.
func (c *Client) UpdateAPIKey(ctx context.Context, id string, payload apitypes.APIKeyPayload) (models.APIKey, error) {
	var out models.APIKey
	err := c.do(ctx, http.MethodPut, "/api/v1/api-keys/"+id, payload, &out)
	return out, err
}

// DeleteAPIKey removes a key and its secret.
func (c *Client) DeleteAPIKey(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/v1/api-keys/"+id, nil, nil)
}

// GetAPIKeySecret returns the stored secret for a key.
func (c *Client) GetAPIKeySecret(ctx context.Context, id string) (string, error) {
	var out apitypes.SecretResponse
	err := c.do(ctx, http.MethodGet, "/api/v1/api-keys/"+id+"/secret", nil, &out)
	return out.Secret, err
}

// TestAPIKey performs a host-side connection test for a stored key.
func (c *Client) TestAPIKey(ctx context.Context, id string) (apitypes.TestConnectionResponse, error) {
	var out apitypes.TestConnectionResponse
	err := c.do(ctx, http.MethodPost, "/api/v1/api-keys/"+id+"/test", nil, &out)
	return out, err
}

// TestAPIKeyPayload tests an unsaved credential payload.
func (c *Client) TestAPIKeyPayload(ctx context.Context, req apitypes.TestConnectionRequest) (apitypes.TestConnectionResponse, error) {
	var out apitypes.TestConnectionResponse
	err := c.do(ctx, http.MethodPost, "/api/v1/api-keys/test", req, &out)
	return out, err
}

// ---- Recipes ----

// ListRecipes returns all recipes.
func (c *Client) ListRecipes(ctx context.Context) ([]models.Recipe, error) {
	var out []models.Recipe
	err := c.do(ctx, http.MethodGet, "/api/v1/recipes", nil, &out)
	return out, err
}

// GetRecipe returns a recipe by ID.
func (c *Client) GetRecipe(ctx context.Context, id string) (models.Recipe, error) {
	var out models.Recipe
	err := c.do(ctx, http.MethodGet, "/api/v1/recipes/"+id, nil, &out)
	return out, err
}

// CreateRecipe creates a recipe.
func (c *Client) CreateRecipe(ctx context.Context, r models.Recipe) (models.Recipe, error) {
	var out models.Recipe
	err := c.do(ctx, http.MethodPost, "/api/v1/recipes", r, &out)
	return out, err
}

// UpdateRecipe updates a recipe.
func (c *Client) UpdateRecipe(ctx context.Context, id string, r models.Recipe) (models.Recipe, error) {
	var out models.Recipe
	err := c.do(ctx, http.MethodPut, "/api/v1/recipes/"+id, r, &out)
	return out, err
}

// DeleteRecipe removes a recipe.
func (c *Client) DeleteRecipe(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/v1/recipes/"+id, nil, nil)
}

// ---- Instances ----

// ListInstances returns instances with live status and disk usage.
func (c *Client) ListInstances(ctx context.Context) ([]apitypes.InstanceView, error) {
	var out []apitypes.InstanceView
	err := c.do(ctx, http.MethodGet, "/api/v1/instances", nil, &out)
	return out, err
}

// GetInstance returns one instance.
func (c *Client) GetInstance(ctx context.Context, id string) (apitypes.InstanceView, error) {
	var out apitypes.InstanceView
	err := c.do(ctx, http.MethodGet, "/api/v1/instances/"+id, nil, &out)
	return out, err
}

// CreateInstance provisions and starts an instance.
func (c *Client) CreateInstance(ctx context.Context, req apitypes.InstanceCreateRequest) (apitypes.InstanceView, error) {
	var out apitypes.InstanceView
	err := c.do(ctx, http.MethodPost, "/api/v1/instances", req, &out)
	return out, err
}

// UpdateInstance updates metadata and/or recreates the container.
func (c *Client) UpdateInstance(ctx context.Context, id string, req apitypes.InstanceUpdateRequest) (apitypes.InstanceView, error) {
	var out apitypes.InstanceView
	err := c.do(ctx, http.MethodPut, "/api/v1/instances/"+id, req, &out)
	return out, err
}

// StartInstance starts a halted instance.
func (c *Client) StartInstance(ctx context.Context, id string) (apitypes.InstanceView, error) {
	var out apitypes.InstanceView
	err := c.do(ctx, http.MethodPost, "/api/v1/instances/"+id+"/start", nil, &out)
	return out, err
}

// HaltInstance halts a running instance.
func (c *Client) HaltInstance(ctx context.Context, id string) (apitypes.InstanceView, error) {
	var out apitypes.InstanceView
	err := c.do(ctx, http.MethodPost, "/api/v1/instances/"+id+"/halt", nil, &out)
	return out, err
}

// DeleteInstance removes an instance and its container.
func (c *Client) DeleteInstance(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/v1/instances/"+id, nil, nil)
}

// InjectInstance copies a host file into a running container.
func (c *Client) InjectInstance(ctx context.Context, id string, req apitypes.InjectRequest) error {
	return c.do(ctx, http.MethodPost, "/api/v1/instances/"+id+"/inject", req, nil)
}

// ResizeExec sets the TTY size of an interactive exec session.
func (c *Client) ResizeExec(ctx context.Context, id, execID string, width, height int) error {
	return c.do(ctx, http.MethodPost, "/api/v1/instances/"+id+"/exec/"+execID+"/resize",
		apitypes.ResizeRequest{Width: width, Height: height}, nil)
}

// ---- System ----

// ListGPUs returns detected host AMD GPUs.
func (c *Client) ListGPUs(ctx context.Context) ([]models.GPU, error) {
	var out apitypes.GPUListResponse
	err := c.do(ctx, http.MethodGet, "/api/v1/system/gpus", nil, &out)
	return out.GPUs, err
}

// SystemStatus returns backend health details.
func (c *Client) SystemStatus(ctx context.Context) (apitypes.SystemStatusResponse, error) {
	var out apitypes.SystemStatusResponse
	err := c.do(ctx, http.MethodGet, "/api/v1/system/status", nil, &out)
	return out, err
}

// Connect opens a new interactive shell in an instance and returns the raw
// bidirectional stream plus the exec ID used for subsequent resizes.
func (c *Client) Connect(ctx context.Context, id string, cols, rows int) (io.ReadWriteCloser, string, error) {
	path := fmt.Sprintf("/api/v1/instances/%s/connect?cols=%d&rows=%d", id, cols, rows)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "tcp")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	rwc, ok := resp.Body.(io.ReadWriteCloser)
	if !ok {
		defer resp.Body.Close()
		msg := strings.TrimSpace(readAll(resp.Body))
		var envelope apitypes.ErrorResponse
		if json.Unmarshal([]byte(msg), &envelope) == nil && envelope.Error != "" {
			msg = envelope.Error
		}
		if msg == "" {
			msg = resp.Status
		}
		return nil, "", &APIError{StatusCode: resp.StatusCode, Message: msg}
	}
	return rwc, resp.Header.Get("X-Vivarium-Exec"), nil
}

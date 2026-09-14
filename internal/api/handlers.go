package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"vivarium/internal/keyring"
	"vivarium/internal/models"
	"vivarium/internal/version"
)

// ---- System ----

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, pingResponse{Status: "ok", Version: version.Version})
}

// ---- Auth ----

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.auth.Status()
	if err != nil {
		writeMappedError(w, err)
		return
	}
	cfg, err := s.store.Config()
	if err != nil {
		writeMappedError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, authStatusResponse{
		Status:      string(status),
		StorageType: string(cfg.SecretStorageType),
	})
}

func (s *Server) handleAuthSetup(w http.ResponseWriter, r *http.Request) {
	var req authSetupRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if err := s.auth.Setup(req.StorageType, req.Password); err != nil {
		writeMappedError(w, err)
		return
	}
	status, _ := s.auth.Status()
	writeJSON(w, http.StatusOK, authStatusResponse{Status: string(status), StorageType: string(req.StorageType)})
}

func (s *Server) handleAuthUnlock(w http.ResponseWriter, r *http.Request) {
	var req authUnlockRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if err := s.auth.Unlock(req.Password); err != nil {
		writeMappedError(w, err)
		return
	}
	status, _ := s.auth.Status()
	writeJSON(w, http.StatusOK, authStatusResponse{Status: string(status)})
}

// ---- Base Images ----

func (s *Server) handleListBaseImages(w http.ResponseWriter, r *http.Request) {
	imgs, err := s.store.ListBaseImages()
	if err != nil {
		writeMappedError(w, err)
		return
	}
	views := make([]baseImageView, len(imgs))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i := range imgs {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			present, size, _ := s.docker.ImageExists(r.Context(), imgs[i].SourcePathOrRepo)
			views[i] = baseImageView{BaseImage: imgs[i], Present: present, SizeBytes: size}
		}(i)
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, views)
}

func (s *Server) handleCreateBaseImage(w http.ResponseWriter, r *http.Request) {
	var img models.BaseImage
	if err := decodeJSON(r, &img); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if img.ID == "" {
		img.ID = models.NewID()
	}
	img.IsInternal = false
	if err := s.store.PutBaseImage(img); err != nil {
		writeMappedError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, img)
}

func (s *Server) handleDeleteBaseImage(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteBaseImage(r.PathValue("id")); err != nil {
		writeMappedError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

var standardImageTags = map[string]string{
	"opencode": "vivarium/ubuntu-24.04-opencode:latest",
	"pi":       "vivarium/ubuntu-24.04-pi:latest",
}

func (s *Server) handleRebuildBaseImages(w http.ResponseWriter, r *http.Request) {
	var req rebuildRequest
	_ = decodeJSON(r, &req)

	keys := []string{"opencode", "pi"}
	if req.Key != "" {
		if _, ok := standardImageTags[req.Key]; !ok {
			writeError(w, http.StatusBadRequest, "unknown standard image "+req.Key)
			return
		}
		keys = []string{req.Key}
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)

	for _, key := range keys {
		fmt.Fprintf(w, "==> Building %s (%s)\n", key, standardImageTags[key])
		if flusher != nil {
			flusher.Flush()
		}
		if err := s.docker.BuildStandardImage(r.Context(), key, standardImageTags[key], w); err != nil {
			fmt.Fprintf(w, "!! build failed: %v\n", err)
			if flusher != nil {
				flusher.Flush()
			}
			return
		}
		fmt.Fprintf(w, "==> Finished %s\n", key)
		if flusher != nil {
			flusher.Flush()
		}
	}
}

// ---- API Keys ----

func (s *Server) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.store.ListAPIKeys()
	if err != nil {
		writeMappedError(w, err)
		return
	}
	views := make([]apiKeyView, len(keys))
	for i, k := range keys {
		_, err := s.auth.Secret(k.ID)
		views[i] = apiKeyView{APIKey: k, HasSecret: err == nil}
	}
	writeJSON(w, http.StatusOK, views)
}

// applyAPIKeyDefaults fills base/mock URLs for non-custom providers when the
// caller omitted them.
func applyAPIKeyDefaults(k *models.APIKey) {
	def := models.ProviderDefaultURL(k.ProviderType)
	if def == "" {
		return
	}
	if strings.TrimSpace(k.BaseURL) == "" {
		k.BaseURL = def
	}
	if strings.TrimSpace(k.MockURL) == "" {
		k.MockURL = def
	}
}

func (s *Server) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	var payload apiKeyPayload
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if payload.ID == "" {
		payload.ID = models.NewID()
	}
	if payload.CreatedAt == 0 {
		payload.CreatedAt = time.Now().Unix()
	}
	if payload.Secret == "" {
		writeError(w, http.StatusBadRequest, "secret is required")
		return
	}
	applyAPIKeyDefaults(&payload.APIKey)
	if err := payload.APIKey.Validate(); err != nil {
		writeMappedError(w, err)
		return
	}
	secrets, err := s.auth.Secrets()
	if err != nil {
		writeMappedError(w, err)
		return
	}
	if err := secrets.StoreSecret(payload.ID, payload.Secret); err != nil {
		writeMappedError(w, err)
		return
	}
	if err := s.store.PutAPIKey(payload.APIKey); err != nil {
		writeMappedError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, payload.APIKey)
}

func (s *Server) handleUpdateAPIKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var payload apiKeyPayload
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	existing, err := s.store.GetAPIKey(id)
	if err != nil {
		writeMappedError(w, err)
		return
	}
	payload.ID = id
	if payload.CreatedAt == 0 {
		payload.CreatedAt = existing.CreatedAt
	}
	applyAPIKeyDefaults(&payload.APIKey)
	if err := payload.APIKey.Validate(); err != nil {
		writeMappedError(w, err)
		return
	}
	if payload.Secret != "" {
		secrets, err := s.auth.Secrets()
		if err != nil {
			writeMappedError(w, err)
			return
		}
		if err := secrets.StoreSecret(id, payload.Secret); err != nil {
			writeMappedError(w, err)
			return
		}
	}
	if err := s.store.PutAPIKey(payload.APIKey); err != nil {
		writeMappedError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, payload.APIKey)
}

func (s *Server) handleDeleteAPIKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.store.GetAPIKey(id); err != nil {
		writeMappedError(w, err)
		return
	}
	if secrets, err := s.auth.Secrets(); err == nil {
		if err := secrets.DeleteSecret(id); err != nil && !errors.Is(err, keyring.ErrNotFound) {
			writeMappedError(w, err)
			return
		}
	}
	if err := s.store.DeleteAPIKey(id); err != nil {
		writeMappedError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGetAPIKeySecret(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.store.GetAPIKey(id); err != nil {
		writeMappedError(w, err)
		return
	}
	secrets, err := s.auth.Secrets()
	if err != nil {
		writeMappedError(w, err)
		return
	}
	secret, err := secrets.GetSecret(id)
	if err != nil {
		writeMappedError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, secretResponse{Secret: secret})
}

func (s *Server) handleTestAPIKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	key, err := s.store.GetAPIKey(id)
	if err != nil {
		writeMappedError(w, err)
		return
	}
	secrets, err := s.auth.Secrets()
	if err != nil {
		writeMappedError(w, err)
		return
	}
	secret, err := secrets.GetSecret(id)
	if err != nil {
		writeMappedError(w, err)
		return
	}
	result := testConnection(r.Context(), key, secret)
	writeJSON(w, http.StatusOK, result)
}

// handleTestAPIKeyPayload tests a credential that may not be saved yet. It
// accepts the secret directly, or falls back to the stored secret for key_id.
func (s *Server) handleTestAPIKeyPayload(w http.ResponseWriter, r *http.Request) {
	var req testConnectionRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if strings.TrimSpace(req.BaseURL) == "" {
		writeError(w, http.StatusBadRequest, "base_url is required")
		return
	}
	secret := req.Secret
	if secret == "" {
		if req.KeyID == "" {
			writeError(w, http.StatusBadRequest, "secret or key_id is required")
			return
		}
		secrets, err := s.auth.Secrets()
		if err != nil {
			writeMappedError(w, err)
			return
		}
		secret, err = secrets.GetSecret(req.KeyID)
		if err != nil {
			writeMappedError(w, err)
			return
		}
	}
	key := models.APIKey{ProviderType: req.ProviderType, BaseURL: req.BaseURL}
	writeJSON(w, http.StatusOK, testConnection(r.Context(), key, secret))
}

func testConnection(ctx context.Context, key models.APIKey, secret string) testConnectionResponse {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	target := models.ProviderTestURL(key.ProviderType, key.BaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return testConnectionResponse{Error: err.Error(), Message: "invalid base URL"}
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	if key.ProviderType == models.ProviderAnthropic {
		req.Header.Set("x-api-key", secret)
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return testConnectionResponse{Error: err.Error(), Message: "unreachable"}
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return testConnectionResponse{OK: true, StatusCode: resp.StatusCode, Message: "valid"}
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return testConnectionResponse{StatusCode: resp.StatusCode, Message: "authentication failed"}
	case resp.StatusCode >= 500:
		return testConnectionResponse{StatusCode: resp.StatusCode, Message: "provider error"}
	default:
		return testConnectionResponse{StatusCode: resp.StatusCode, Message: "reachable"}
	}
}

// ---- Recipes ----

func (s *Server) handleListRecipes(w http.ResponseWriter, r *http.Request) {
	recipes, err := s.store.ListRecipes()
	if err != nil {
		writeMappedError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, recipes)
}

func (s *Server) handleCreateRecipe(w http.ResponseWriter, r *http.Request) {
	var recipe models.Recipe
	if err := decodeJSON(r, &recipe); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if recipe.ID == "" {
		recipe.ID = models.NewID()
	}
	if err := s.store.PutRecipe(recipe); err != nil {
		writeMappedError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, recipe)
}

func (s *Server) handleGetRecipe(w http.ResponseWriter, r *http.Request) {
	recipe, err := s.store.GetRecipe(r.PathValue("id"))
	if err != nil {
		writeMappedError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, recipe)
}

func (s *Server) handleUpdateRecipe(w http.ResponseWriter, r *http.Request) {
	var recipe models.Recipe
	if err := decodeJSON(r, &recipe); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	recipe.ID = r.PathValue("id")
	if err := s.store.PutRecipe(recipe); err != nil {
		writeMappedError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, recipe)
}

func (s *Server) handleDeleteRecipe(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteRecipe(r.PathValue("id")); err != nil {
		writeMappedError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- System ----

func (s *Server) handleListGPUs(w http.ResponseWriter, r *http.Request) {
	gpus, err := s.gpuEnum.Enumerate()
	if err != nil {
		writeMappedError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, gpuListResponse{GPUs: gpus})
}

func (s *Server) handleSystemStatus(w http.ResponseWriter, r *http.Request) {
	resp := systemStatusResponse{SocketPath: s.env.SocketPath(), Version: version.Version}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := s.docker.Ping(ctx); err == nil {
		resp.DockerOK = true
		if v, err := s.docker.Version(ctx); err == nil {
			resp.DockerVersion = v
		}
	}

	if secrets, err := s.auth.Secrets(); err == nil {
		resp.KeyringAvailable = secrets.Kind() == keyring.KindLibsecret || s.auth.Vault().IsUnlocked()
	}
	if status, err := s.auth.Status(); err == nil {
		resp.EncryptionMode = string(status)
	}
	resp.KeyringCollection = s.auth.KeyringCollection()
	if _, err := os.Stat(filepath.Join(s.env.CertsDir(), "vivarium-ca.crt")); err == nil {
		resp.CACertPresent = true
	}
	writeJSON(w, http.StatusOK, resp)
}

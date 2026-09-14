package api

import "vivarium/internal/apitypes"

// The API handler uses these names; the canonical definitions live in
// internal/apitypes so the frontend client can share them.
type (
	authStatusResponse     = apitypes.AuthStatusResponse
	authSetupRequest       = apitypes.AuthSetupRequest
	authUnlockRequest      = apitypes.AuthUnlockRequest
	apiKeyPayload          = apitypes.APIKeyPayload
	secretResponse         = apitypes.SecretResponse
	instanceCreateRequest  = apitypes.InstanceCreateRequest
	instanceUpdateRequest  = apitypes.InstanceUpdateRequest
	instanceView           = apitypes.InstanceView
	injectRequest          = apitypes.InjectRequest
	testConnectionResponse = apitypes.TestConnectionResponse
	gpuListResponse        = apitypes.GPUListResponse
	systemStatusResponse   = apitypes.SystemStatusResponse
	errorResponse          = apitypes.ErrorResponse
	rebuildRequest         = apitypes.RebuildRequest
	pingResponse           = apitypes.PingResponse
	baseImageView          = apitypes.BaseImageView
	resizeRequest          = apitypes.ResizeRequest
	apiKeyView             = apitypes.APIKeyView
	testConnectionRequest  = apitypes.TestConnectionRequest
)

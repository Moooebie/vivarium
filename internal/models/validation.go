package models

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

// ValidationError describes a single field-level validation failure.
type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	if e.Field == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

func required(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return ValidationError{Field: field, Message: "is required"}
	}
	return nil
}

func validURL(field, value string) error {
	if err := required(field, value); err != nil {
		return err
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ValidationError{Field: field, Message: "must be an absolute URL"}
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ValidationError{Field: field, Message: "must use http or https"}
	}
	return nil
}

func absolutePath(field, value string) error {
	if err := required(field, value); err != nil {
		return err
	}
	if !filepath.IsAbs(value) {
		return ValidationError{Field: field, Message: "must be an absolute path"}
	}
	return nil
}

// Validate checks a BaseImage for correctness.
func (b BaseImage) Validate() error {
	if err := required("id", b.ID); err != nil {
		return err
	}
	if err := required("name", b.Name); err != nil {
		return err
	}
	switch b.SourceType {
	case SourceStandard, SourceDockerHub, SourceDockerfile:
	default:
		return ValidationError{Field: "source_type", Message: "must be standard, dockerhub or dockerfile"}
	}
	if err := required("source_path_or_repo", b.SourcePathOrRepo); err != nil {
		return err
	}
	if err := required("default_user", b.DefaultUser); err != nil {
		return err
	}
	return nil
}

// Validate checks an APIKey for correctness.
func (k APIKey) Validate() error {
	if err := required("id", k.ID); err != nil {
		return err
	}
	if err := required("name", k.Name); err != nil {
		return err
	}
	switch k.ProviderType {
	case ProviderOpenAI, ProviderAnthropic, ProviderDeepSeek, ProviderOpenRouter, ProviderCustom:
	default:
		return ValidationError{Field: "provider_type", Message: "unknown provider type"}
	}
	if err := validURL("base_url", k.BaseURL); err != nil {
		return err
	}
	if err := validURL("mock_url", k.MockURL); err != nil {
		return err
	}
	if k.CreatedAt <= 0 {
		return ValidationError{Field: "created_at", Message: "must be a positive unix timestamp"}
	}
	return nil
}

// Validate checks a Resource for correctness.
func (r Resource) Validate() error {
	if err := absolutePath("host_source_path", r.HostSourcePath); err != nil {
		return err
	}
	if err := absolutePath("guest_target_path", r.GuestTargetPath); err != nil {
		return err
	}
	if strings.TrimSpace(r.FileMode) == "" {
		return ValidationError{Field: "file_mode", Message: "is required"}
	}
	if _, err := strconv.ParseUint(r.FileMode, 8, 32); err != nil {
		return ValidationError{Field: "file_mode", Message: "must be an octal permission string such as 0644"}
	}
	return nil
}

// Validate checks a Mount for correctness.
func (m Mount) Validate() error {
	if err := absolutePath("host_path", m.HostPath); err != nil {
		return err
	}
	if err := absolutePath("guest_path", m.GuestPath); err != nil {
		return err
	}
	switch m.Mode {
	case MountReadOnly, MountReadWrite:
	default:
		return ValidationError{Field: "mode", Message: "must be ro or rw"}
	}
	return nil
}

// Validate checks a GPU for correctness.
func (g GPU) Validate() error {
	if err := required("device_id", g.DeviceID); err != nil {
		return err
	}
	if err := required("name", g.Name); err != nil {
		return err
	}
	if err := required("render_node", g.RenderNode); err != nil {
		return err
	}
	return nil
}

// Validate checks a Recipe for correctness, including the constraint that no
// two API endpoints may share a mock_url.
func (r Recipe) Validate() error {
	if err := required("id", r.ID); err != nil {
		return err
	}
	if err := required("name", r.Name); err != nil {
		return err
	}
	if err := required("base_image_id", r.BaseImageID); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(r.APIEndpoints))
	for i, k := range r.APIEndpoints {
		if err := k.Validate(); err != nil {
			return fmt.Errorf("api_endpoints[%d]: %w", i, err)
		}
		if _, dup := seen[k.MockURL]; dup {
			return ValidationError{Field: "api_endpoints", Message: "duplicate mock_url: " + k.MockURL}
		}
		seen[k.MockURL] = struct{}{}
	}
	for i, res := range r.Resources {
		if err := res.Validate(); err != nil {
			return fmt.Errorf("resources[%d]: %w", i, err)
		}
	}
	for i, m := range r.DefaultMounts {
		if err := m.Validate(); err != nil {
			return fmt.Errorf("default_mounts[%d]: %w", i, err)
		}
	}
	for i, g := range r.GPUs {
		if err := g.Validate(); err != nil {
			return fmt.Errorf("gpus[%d]: %w", i, err)
		}
	}
	return nil
}

// Validate checks an InstanceEndpoint for correctness.
func (e InstanceEndpoint) Validate() error {
	if err := required("key_id", e.KeyID); err != nil {
		return err
	}
	if err := validURL("base_url", e.BaseURL); err != nil {
		return err
	}
	if err := validURL("mock_url", e.MockURL); err != nil {
		return err
	}
	if err := required("token", e.Token); err != nil {
		return err
	}
	return nil
}

// Validate checks an Instance for correctness.
func (i Instance) Validate() error {
	if err := required("id", i.ID); err != nil {
		return err
	}
	if err := required("name", i.Name); err != nil {
		return err
	}
	switch i.Status {
	case StatusRunning, StatusHalted, StatusBuilding, StatusError:
	default:
		return ValidationError{Field: "status", Message: "unknown instance status"}
	}
	if err := required("base_image_tag", i.BaseImageTag); err != nil {
		return err
	}
	for idx, m := range i.Mounts {
		if err := m.Validate(); err != nil {
			return fmt.Errorf("mounts[%d]: %w", idx, err)
		}
	}
	for idx, g := range i.GPUs {
		if err := g.Validate(); err != nil {
			return fmt.Errorf("gpus[%d]: %w", idx, err)
		}
	}
	for idx, res := range i.Resources {
		if err := res.Validate(); err != nil {
			return fmt.Errorf("resources[%d]: %w", idx, err)
		}
	}
	seenMock := make(map[string]struct{}, len(i.Endpoints))
	seenToken := make(map[string]struct{}, len(i.Endpoints))
	for idx, e := range i.Endpoints {
		if err := e.Validate(); err != nil {
			return fmt.Errorf("endpoints[%d]: %w", idx, err)
		}
		if _, dup := seenMock[e.MockURL]; dup {
			return ValidationError{Field: "endpoints", Message: "duplicate mock_url: " + e.MockURL}
		}
		if _, dup := seenToken[e.Token]; dup {
			return ValidationError{Field: "endpoints", Message: "duplicate token"}
		}
		seenMock[e.MockURL] = struct{}{}
		seenToken[e.Token] = struct{}{}
	}
	return nil
}

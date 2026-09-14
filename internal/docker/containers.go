package docker

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// ListContainers returns containers known to the daemon.
func (c *Client) ListContainers(ctx context.Context, all bool) ([]ContainerSummary, error) {
	path := "/containers/json?all=" + strconv.FormatBool(all)
	var out []ContainerSummary
	if err := c.getJSON(ctx, path, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// InspectContainer returns detailed state. When size is true the response
// includes SizeRw and SizeRootFs.
func (c *Client) InspectContainer(ctx context.Context, id string, size bool) (*ContainerInspect, error) {
	path := "/containers/" + escapePath(id) + "/json?size=" + strconv.FormatBool(size)
	var out ContainerInspect
	if err := c.getJSON(ctx, path, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateContainer creates a container and returns its ID.
func (c *Client) CreateContainer(ctx context.Context, name string, req containerCreateRequest) (string, error) {
	path := "/containers/create"
	if name != "" {
		path += "?name=" + url.QueryEscape(name)
	}
	var out containerCreateResponse
	if err := c.postJSON(ctx, path, req, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// StartContainer starts a created container.
func (c *Client) StartContainer(ctx context.Context, id string) error {
	return c.postJSON(ctx, "/containers/"+escapePath(id)+"/start", nil, nil)
}

// StopContainer stops a running container with a graceful timeout.
func (c *Client) StopContainer(ctx context.Context, id string, timeoutSec int) error {
	path := "/containers/" + escapePath(id) + "/stop?t=" + strconv.Itoa(timeoutSec)
	return c.postJSON(ctx, path, nil, nil)
}

// RemoveContainer removes a container, optionally forcing and removing volumes.
func (c *Client) RemoveContainer(ctx context.Context, id string, force, volumes bool) error {
	path := "/containers/" + escapePath(id) + "?force=" + strconv.FormatBool(force) +
		"&v=" + strconv.FormatBool(volumes)
	return c.delete(ctx, path)
}

// CopyArchive writes a tar stream into a container filesystem path using the
// Engine Copy Archive API.
func (c *Client) CopyArchive(ctx context.Context, id, dstPath string, content io.Reader) error {
	path := "/containers/" + escapePath(id) + "/archive?path=" + url.QueryEscape(dstPath)
	resp, err := c.do(ctx, http.MethodPut, path, content, map[string]string{
		"Content-Type": "application/x-tar",
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

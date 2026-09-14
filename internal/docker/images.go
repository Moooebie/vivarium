package docker

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

//go:embed dockerfiles/*.Dockerfile
var dockerfilesFS embed.FS

// StandardDockerfile returns an embedded Dockerfile by key (e.g. "opencode").
func StandardDockerfile(key string) ([]byte, error) {
	data, err := dockerfilesFS.ReadFile("dockerfiles/" + key + ".Dockerfile")
	if err != nil {
		return nil, fmt.Errorf("unknown standard dockerfile %q: %w", key, err)
	}
	return data, nil
}

// ImageExists reports whether an image reference is present locally, along with
// its size in bytes when found.
func (c *Client) ImageExists(ctx context.Context, ref string) (bool, int64, error) {
	var img imageInspect
	err := c.getJSON(ctx, "/images/"+escapePath(ref)+"/json", &img)
	if err != nil {
		if IsNotFound(err) {
			return false, 0, nil
		}
		return false, 0, err
	}
	return true, img.Size, nil
}

// BuildFromDockerfile builds an image from a Dockerfile, streaming build logs
// to logs (which may be nil).
func (c *Client) BuildFromDockerfile(ctx context.Context, dockerfile []byte, tag string, logs io.Writer) error {
	contextTar, err := buildContextTar(map[string][]byte{"Dockerfile": dockerfile})
	if err != nil {
		return err
	}
	q := url.Values{}
	q.Set("t", tag)
	q.Set("dockerfile", "Dockerfile")
	q.Set("rm", "1")
	q.Set("forcerm", "1")
	resp, err := c.do(ctx, http.MethodPost, "/build?"+q.Encode(), bytes.NewReader(contextTar),
		map[string]string{"Content-Type": "application/x-tar"})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if logs != nil {
		_, _ = io.Copy(logs, resp.Body)
	} else {
		_, _ = io.Copy(io.Discard, resp.Body)
	}
	return nil
}

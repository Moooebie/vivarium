// Package docker is a focused Docker Engine API client and controller. It
// speaks the Engine REST API directly over the Docker Unix socket rather than
// pulling in the full SDK.
package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultSocket is the standard Docker Engine socket.
const DefaultSocket = "/var/run/docker.sock"

// apiVersion pins the Engine API version used for requests.
const apiVersion = "v1.44"

// APIError is a structured error returned by the Docker daemon.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("docker api %d: %s", e.StatusCode, e.Message)
}

// IsNotFound reports whether the error is an HTTP 404 from the daemon.
func IsNotFound(err error) bool {
	var apiErr *APIError
	if ok := asAPIError(err, &apiErr); ok {
		return apiErr.StatusCode == http.StatusNotFound
	}
	return false
}

// IsConflict reports whether the error is an HTTP 409 from the daemon.
func IsConflict(err error) bool {
	var apiErr *APIError
	if ok := asAPIError(err, &apiErr); ok {
		return apiErr.StatusCode == http.StatusConflict
	}
	return false
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

// Client is a minimal Docker Engine API client.
type Client struct {
	http    *http.Client
	baseURL string
}

// NewClient returns a client bound to the Docker socket at socketPath.
func NewClient(socketPath string) *Client {
	if socketPath == "" {
		socketPath = DefaultSocket
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 10 * time.Second}
			return d.DialContext(ctx, "unix", socketPath)
		},
	}
	return &Client{
		http:    &http.Client{Transport: transport},
		baseURL: "http://docker",
	}
}

// NewClientForURL returns a client for tests, pointed at an arbitrary base URL.
func NewClientForURL(baseURL string, hc *http.Client) *Client {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{http: hc, baseURL: strings.TrimRight(baseURL, "/")}
}

func (c *Client) do(ctx context.Context, method, path string, body io.Reader, headers map[string]string) (*http.Response, error) {
	u := c.baseURL + "/" + apiVersion + path
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	req.Host = "docker"
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		msg := strings.TrimSpace(readLimited(resp.Body, 4096))
		if msg == "" {
			msg = resp.Status
		}
		var apiErr struct {
			Message string `json:"message"`
		}
		if json.Unmarshal([]byte(msg), &apiErr) == nil && apiErr.Message != "" {
			msg = apiErr.Message
		}
		return nil, &APIError{StatusCode: resp.StatusCode, Message: msg}
	}
	return resp, nil
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	resp, err := c.do(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) postJSON(ctx context.Context, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	headers := map[string]string{"Content-Type": "application/json"}
	resp, err := c.do(ctx, http.MethodPost, path, body, headers)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) delete(ctx context.Context, path string) error {
	resp, err := c.do(ctx, http.MethodDelete, path, nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return nil
}

// Ping checks that the daemon is reachable.
func (c *Client) Ping(ctx context.Context) error {
	resp, err := c.do(ctx, http.MethodGet, "/_ping", nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return nil
}

// Version returns the daemon version string.
func (c *Client) Version(ctx context.Context) (string, error) {
	var v struct {
		Version string `json:"Version"`
	}
	if err := c.getJSON(ctx, "/version", &v); err != nil {
		return "", err
	}
	return v.Version, nil
}

func readLimited(r io.Reader, n int64) string {
	data, _ := io.ReadAll(io.LimitReader(r, n))
	return string(data)
}

// escapePath escapes a path segment for use in a URL path.
func escapePath(s string) string { return url.PathEscape(s) }

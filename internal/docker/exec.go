package docker

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

// Exec runs a command in a container and returns its combined output. The
// container must be running.
func (c *Client) Exec(ctx context.Context, id string, cmd []string) (string, error) {
	var created execCreateResponse
	create := execCreateRequest{AttachStdout: true, AttachStderr: true, Cmd: cmd}
	if err := c.postJSON(ctx, "/containers/"+escapePath(id)+"/exec", create, &created); err != nil {
		return "", err
	}
	body, err := json.Marshal(execStartRequest{Detach: false, Tty: false})
	if err != nil {
		return "", err
	}
	resp, err := c.do(ctx, http.MethodPost, "/exec/"+escapePath(created.ID)+"/start",
		bytes.NewReader(body), map[string]string{"Content-Type": "application/json"})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return demuxStream(raw), nil
}

// ExecStatus runs a command in a container and returns its exit code and
// combined output.
func (c *Client) ExecStatus(ctx context.Context, id string, cmd []string) (int, string, error) {
	var created execCreateResponse
	create := execCreateRequest{AttachStdout: true, AttachStderr: true, Cmd: cmd}
	if err := c.postJSON(ctx, "/containers/"+escapePath(id)+"/exec", create, &created); err != nil {
		return -1, "", err
	}
	body, err := json.Marshal(execStartRequest{Detach: false, Tty: false})
	if err != nil {
		return -1, "", err
	}
	resp, err := c.do(ctx, http.MethodPost, "/exec/"+escapePath(created.ID)+"/start",
		bytes.NewReader(body), map[string]string{"Content-Type": "application/json"})
	if err != nil {
		return -1, "", err
	}
	raw, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	if readErr != nil {
		return -1, "", readErr
	}
	var inspect execInspectResponse
	if err := c.getJSON(ctx, "/exec/"+escapePath(created.ID)+"/json", &inspect); err != nil {
		return -1, demuxStream(raw), err
	}
	return inspect.ExitCode, demuxStream(raw), nil
}

// ExecDetached starts a command in a container without waiting for it, running
// as the given user (empty = container default).
func (c *Client) ExecDetached(ctx context.Context, id, user string, cmd []string) error {
	var created execCreateResponse
	create := execCreateRequest{Cmd: cmd, User: user}
	if err := c.postJSON(ctx, "/containers/"+escapePath(id)+"/exec", create, &created); err != nil {
		return err
	}
	return c.postJSON(ctx, "/exec/"+escapePath(created.ID)+"/start",
		execStartRequest{Detach: true, Tty: false}, nil)
}

// CreateExec creates an exec instance with attached stdio and returns its ID.
// When tty is true the stream is a raw TTY stream; otherwise it is multiplexed.
func (c *Client) CreateExec(ctx context.Context, id string, cmd []string, tty bool) (string, error) {
	var created execCreateResponse
	create := execCreateRequest{
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          tty,
		Cmd:          cmd,
	}
	if err := c.postJSON(ctx, "/containers/"+escapePath(id)+"/exec", create, &created); err != nil {
		return "", err
	}
	return created.ID, nil
}

// StartExec starts an exec instance and returns its upgraded bidirectional
// stream. The caller owns the returned stream and must close it.
func (c *Client) StartExec(ctx context.Context, execID string, tty bool) (io.ReadWriteCloser, error) {
	body, err := json.Marshal(execStartRequest{Detach: false, Tty: tty})
	if err != nil {
		return nil, err
	}
	resp, err := c.do(ctx, http.MethodPost, "/exec/"+escapePath(execID)+"/start",
		bytes.NewReader(body), map[string]string{
			"Content-Type": "application/json",
			"Connection":   "Upgrade",
			"Upgrade":      "tcp",
		})
	if err != nil {
		return nil, err
	}
	rwc, ok := resp.Body.(io.ReadWriteCloser)
	if !ok {
		resp.Body.Close()
		return nil, fmt.Errorf("docker did not upgrade the exec connection")
	}
	return rwc, nil
}

// ResizeExec sets the TTY size of an exec instance.
func (c *Client) ResizeExec(ctx context.Context, execID string, height, width int) error {
	path := "/exec/" + escapePath(execID) + "/resize?h=" + strconv.Itoa(height) + "&w=" + strconv.Itoa(width)
	return c.postJSON(ctx, path, nil, nil)
}

// demuxStream decodes the Docker raw-stream framing used for non-TTY exec.
// If the data is not framed it is returned as-is.
func demuxStream(b []byte) string {
	original := b
	var out bytes.Buffer
	for len(b) > 0 {
		if len(b) < 8 {
			return string(original)
		}
		size := int(binary.BigEndian.Uint32(b[4:8]))
		if size < 0 || len(b) < 8+size {
			return string(original)
		}
		out.Write(b[8 : 8+size])
		b = b[8+size:]
	}
	return out.String()
}

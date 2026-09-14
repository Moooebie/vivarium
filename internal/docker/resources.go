package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
)

// buildTarPath produces an in-memory tar archive for a single file, including
// explicit parent directory entries so extraction succeeds even when the
// destination directories do not yet exist. Entry names are relative to "/".
func buildTarPath(guestPath string, content []byte, mode int64) ([]byte, error) {
	clean := strings.TrimPrefix(path.Clean("/"+guestPath), "/")
	if clean == "" {
		return nil, fmt.Errorf("invalid guest path %q", guestPath)
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	parts := strings.Split(clean, "/")
	for i := 1; i < len(parts); i++ {
		dir := strings.Join(parts[:i], "/") + "/"
		if err := tw.WriteHeader(&tar.Header{Name: dir, Mode: 0o755, Typeflag: tar.TypeDir}); err != nil {
			return nil, err
		}
	}
	if err := tw.WriteHeader(&tar.Header{Name: clean, Mode: mode, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		return nil, err
	}
	if _, err := tw.Write(content); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// buildContextTar produces an in-memory tar archive from a set of named files,
// suitable as a Docker build context.
func buildContextTar(files map[string][]byte) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, content := range files {
		hdr := &tar.Header{
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if _, err := tw.Write(content); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ParseFileMode converts an octal string such as "0644" into a numeric mode.
func ParseFileMode(s string) (int64, error) {
	v, err := strconv.ParseInt(s, 8, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid file mode %q: %w", s, err)
	}
	return v, nil
}

// InjectFile writes content to guestPath inside the container, creating any
// missing parent directories via explicit tar entries.
func (c *Client) InjectFile(ctx context.Context, id, guestPath string, content []byte, mode int64) error {
	archive, err := buildTarPath(guestPath, content, mode)
	if err != nil {
		return err
	}
	return c.CopyArchive(ctx, id, "/", bytes.NewReader(archive))
}

// InjectTar writes a pre-built tar stream into a container directory.
func (c *Client) InjectTar(ctx context.Context, id, dstDir string, tarStream io.Reader) error {
	return c.CopyArchive(ctx, id, dstDir, tarStream)
}

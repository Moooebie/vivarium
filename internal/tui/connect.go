package tui

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// connectDoneMsg reports that an interactive shell session ended.
type connectDoneMsg struct{ err error }

// connectCmd is a tea.ExecCommand that pipes terminal stdio to a fresh exec
// shell inside a container. Each invocation opens an independent session.
type connectCmd struct {
	client API
	id     string
	ctx    context.Context
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

// Run implements tea.ExecCommand.
func (c *connectCmd) Run() error {
	fd := int(os.Stdin.Fd())
	if f, ok := c.stdin.(*os.File); ok {
		fd = int(f.Fd())
	}
	// Capture the initial size before opening the session so the guest shell
	// starts at the right dimensions.
	cols, rows := 0, 0
	if term.IsTerminal(fd) {
		if w, h, err := term.GetSize(fd); err == nil {
			cols, rows = w, h
		}
	}

	stream, execID, err := c.client.Connect(c.ctx, c.id, cols, rows)
	if err != nil {
		return err
	}
	defer stream.Close()

	// Apply the size explicitly: the exec must be running before it can be
	// resized, so the query-param resize during Connect may have been dropped.
	c.resizeExec(fd, execID)

	// The container shell needs per-keystroke input, not line-buffered cooked
	// mode, so put the local terminal into raw mode for the session.
	if term.IsTerminal(fd) {
		if oldState, err := term.MakeRaw(fd); err == nil {
			defer term.Restore(fd, oldState)
		}
	}

	// Keep the container PTY sized to the local terminal.
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	go func() {
		for range winch {
			c.resizeExec(fd, execID)
		}
	}()

	// Forward stdin until the session ends. The reader polls with a timeout so
	// the goroutine exits promptly when the remote closes instead of lingering
	// and swallowing the first key after returning to the TUI.
	done := make(chan struct{})
	stdinDone := make(chan struct{})
	go func() {
		c.pumpStdin(stream, done)
		close(stdinDone)
	}()
	_, _ = io.Copy(c.stdout, stream)
	close(done)
	select {
	case <-stdinDone:
	case <-time.After(500 * time.Millisecond):
	}
	return nil
}

// pumpStdin forwards local input to the stream until done is closed.
func (c *connectCmd) pumpStdin(stream io.Writer, done <-chan struct{}) {
	f, ok := c.stdin.(*os.File)
	if !ok {
		_, _ = io.Copy(stream, c.stdin)
		return
	}
	fd := int32(f.Fd())
	buf := make([]byte, 4096)
	pfd := []unix.PollFd{{Fd: fd, Events: unix.POLLIN}}
	for {
		select {
		case <-done:
			return
		default:
		}
		n, err := unix.Poll(pfd, 200)
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			return
		}
		if n == 0 {
			continue // timeout; re-check done
		}
		rn, rerr := unix.Read(int(fd), buf)
		if rn > 0 {
			if _, werr := stream.Write(buf[:rn]); werr != nil {
				return
			}
		}
		if rerr != nil {
			if rerr == unix.EAGAIN {
				continue
			}
			return
		}
	}
}

func (c *connectCmd) resizeExec(fd int, execID string) {
	width, height, err := term.GetSize(fd)
	if err != nil || width == 0 || height == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = c.client.ResizeExec(ctx, c.id, execID, width, height)
}

// SetStdin implements tea.ExecCommand.
func (c *connectCmd) SetStdin(r io.Reader) { c.stdin = r }

// SetStdout implements tea.ExecCommand.
func (c *connectCmd) SetStdout(w io.Writer) { c.stdout = w }

// SetStderr implements tea.ExecCommand.
func (c *connectCmd) SetStderr(w io.Writer) { c.stderr = w }

package tui

import (
	"context"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"vivarium/internal/apitypes"
	"vivarium/internal/models"
)

type authState int

const (
	authLoading authState = iota
	authPassword
	authUnlock
	authError
)

type authScreen struct {
	app     *App
	state   authState
	pw      textinput.Model
	confirm textinput.Model
	focus   int
	err     string
}

type authStatusMsg struct {
	resp apitypes.AuthStatusResponse
	err  error
}

type authResultMsg struct{ err error }

func newAuthScreen(app *App) *authScreen {
	s := &authScreen{app: app, state: authLoading}
	s.pw = textinput.New()
	s.pw.Placeholder = "Master Password"
	s.pw.EchoMode = textinput.EchoPassword
	s.pw.CharLimit = 256
	s.confirm = textinput.New()
	s.confirm.Placeholder = "Confirm Master Password"
	s.confirm.EchoMode = textinput.EchoPassword
	s.confirm.CharLimit = 256
	return s
}

func (s *authScreen) ID() string    { return "auth" }
func (s *authScreen) Title() string { return "Encryption Setup" }

func (s *authScreen) CapturingInput() bool {
	return s.state == authPassword || s.state == authUnlock
}

func (s *authScreen) Init() tea.Cmd {
	return func() tea.Msg {
		resp, err := s.app.ctx.Client.AuthStatus(context.Background())
		return authStatusMsg{resp: resp, err: err}
	}
}

func (s *authScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case authStatusMsg:
		if msg.err != nil {
			s.state, s.err = authError, msg.err.Error()
			return s, nil
		}
		switch msg.resp.Status {
		case "unconfigured":
			s.state = authPassword
			s.pw.Focus()
		case "locked":
			s.state = authUnlock
			s.pw.Focus()
		default:
			return s, resetTo(newMainScreen(s.app))
		}
		return s, nil
	case authResultMsg:
		if msg.err != nil {
			s.err = msg.err.Error()
			return s, setBusy(false, "")
		}
		return s, tea.Batch(setBusy(false, ""), resetTo(newMainScreen(s.app)))
	case tea.KeyMsg:
		return s.handleKey(msg)
	}
	return s, nil
}

func (s *authScreen) handleKey(msg tea.KeyMsg) (Screen, tea.Cmd) {
	switch s.state {
	case authPassword:
		switch msg.String() {
		case "esc":
			s.pw.Blur()
			s.confirm.Blur()
			return s, nil
		case "tab", "down":
			s.focus = 1
			s.pw.Blur()
			s.confirm.Focus()
			return s, nil
		case "shift+tab", "up":
			s.focus = 0
			s.confirm.Blur()
			s.pw.Focus()
			return s, nil
		case "enter":
			if s.focus == 0 {
				s.focus = 1
				s.pw.Blur()
				s.confirm.Focus()
				return s, nil
			}
			if strings.TrimSpace(s.pw.Value()) == "" {
				s.err = "master password must not be empty"
				return s, nil
			}
			if s.pw.Value() != s.confirm.Value() {
				s.err = "passwords do not match"
				return s, nil
			}
			s.err = ""
			return s, s.doSetup(s.pw.Value())
		}
		var cmd tea.Cmd
		if s.focus == 0 {
			s.pw, cmd = s.pw.Update(msg)
		} else {
			s.confirm, cmd = s.confirm.Update(msg)
		}
		return s, cmd
	case authUnlock:
		if msg.String() == "enter" {
			s.err = ""
			return s, s.doUnlock(s.pw.Value())
		}
		var cmd tea.Cmd
		s.pw, cmd = s.pw.Update(msg)
		return s, cmd
	case authError:
		if msg.String() == "enter" || msg.String() == "r" {
			s.state = authLoading
			s.err = ""
			return s, s.Init()
		}
	}
	return s, nil
}

func (s *authScreen) doSetup(password string) tea.Cmd {
	return tea.Batch(setBusy(true, "Configuring credentials"), func() tea.Msg {
		_, err := s.app.ctx.Client.AuthSetup(context.Background(), models.SecretVault, password)
		return authResultMsg{err: err}
	})
}

func (s *authScreen) doUnlock(password string) tea.Cmd {
	return tea.Batch(setBusy(true, "Unlocking vault"), func() tea.Msg {
		_, err := s.app.ctx.Client.AuthUnlock(context.Background(), password)
		return authResultMsg{err: err}
	})
}

func (s *authScreen) View() string {
	t := s.app.theme
	var b strings.Builder
	b.WriteString(t.Title.Render("Vivarium — Encryption Setup") + "\n\n")

	switch s.state {
	case authLoading:
		b.WriteString(t.Dim.Render("Contacting backend…"))
	case authPassword:
		b.WriteString("Set a master password for the local encrypted vault.\n" +
			"Secrets are protected with Argon2id + AES-256-GCM.\n\n")
		b.WriteString(label(s, 0, "Master Password") + s.pw.View() + "\n")
		b.WriteString(label(s, 1, "Confirm Password") + s.confirm.View() + "\n")
		if s.err != "" {
			b.WriteString("\n" + t.Error.Render(s.err) + "\n")
		}
		b.WriteString("\n" + t.Dim.Render("Tab switch   Enter next/confirm"))
	case authUnlock:
		b.WriteString("Enter the master password to unlock the vault.\n\n")
		b.WriteString(s.pw.View() + "\n")
		if s.err != "" {
			b.WriteString("\n" + t.Error.Render(s.err) + "\n")
			b.WriteString(t.Dim.Render("Try again or run 'vivarium --reset-credentials'"))
		} else {
			b.WriteString("\n" + t.Dim.Render("Enter unlock"))
		}
	case authError:
		b.WriteString(t.Error.Render("Backend error: "+s.err) + "\n\n")
		b.WriteString(t.Dim.Render("Press Enter to retry"))
	}
	return b.String()
}

func label(s *authScreen, index int, text string) string {
	style := s.app.theme.FieldLabel
	if s.focus == index {
		style = s.app.theme.FieldFocus
	}
	return style.Render(text+": ") + "\n"
}

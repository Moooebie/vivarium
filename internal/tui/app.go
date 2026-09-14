package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// App is the root Bubble Tea model. It owns the screen stack and modal.
type App struct {
	ctx       *Context
	theme     Theme
	stack     []Screen
	confirm   *confirmState
	width     int
	height    int
	status    string
	statusErr bool
	busy      bool
	busyLabel string
	spinner   spinner.Model
}

func newApp(ctx *Context) *App {
	a := &App{ctx: ctx, theme: DefaultTheme(), spinner: spinner.New()}
	a.spinner.Spinner = spinner.Dot
	a.stack = []Screen{newAuthScreen(a)}
	return a
}

// Init implements tea.Model.
func (a *App) Init() tea.Cmd { return a.top().Init() }

// Update implements tea.Model.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		return a, nil
	case pushMsg:
		a.stack = append(a.stack, msg.screen)
		return a, msg.screen.Init()
	case popMsg:
		if len(a.stack) > 1 {
			a.stack = a.stack[:len(a.stack)-1]
		}
		// Refresh the screen we returned to so newly created items appear.
		if r, ok := a.top().(reloader); ok {
			return a, r.Reload()
		}
		return a, nil
	case replaceMsg:
		a.stack[len(a.stack)-1] = msg.screen
		return a, msg.screen.Init()
	case resetStackMsg:
		a.stack = []Screen{msg.screen}
		return a, msg.screen.Init()
	case customizeDoneMsg:
		// Pop the ephemeral editor and forward the completion to its parent.
		if len(a.stack) > 1 {
			a.stack = a.stack[:len(a.stack)-1]
		}
		next, cmd := a.top().Update(msg)
		a.stack[len(a.stack)-1] = next
		return a, cmd
	case statusMsg:
		a.status, a.statusErr = msg.text, msg.err
		return a, nil
	case busyMsg:
		a.busy, a.busyLabel = msg.on, msg.label
		if msg.on {
			return a, a.spinner.Tick
		}
		return a, nil
	case spinner.TickMsg:
		if !a.busy {
			return a, nil
		}
		var cmd tea.Cmd
		a.spinner, cmd = a.spinner.Update(msg)
		return a, cmd
	case confirmMsg:
		a.confirm = &confirmState{prompt: msg.prompt, onYes: msg.onYes}
		return a, nil
	case tea.KeyMsg:
		return a.handleKey(msg)
	}
	return a.delegate(msg)
}

func (a *App) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if a.confirm != nil {
		switch msg.String() {
		case "y", "Y":
			c := a.confirm
			a.confirm = nil
			if c.onYes != nil {
				return a, c.onYes()
			}
		case "n", "N", "esc":
			a.confirm = nil
		}
		return a, nil
	}
	if msg.String() == "ctrl+c" {
		if len(a.stack) <= 1 {
			return a, tea.Quit
		}
		return a, confirm("Terminate Vivarium session?", func() tea.Cmd { return tea.Quit })
	}
	if c, ok := a.top().(inputCapturer); ok && c.CapturingInput() {
		return a.delegate(msg)
	}
	if msg.String() == "q" {
		if len(a.stack) <= 1 {
			return a, tea.Quit
		}
		return a, confirm("Terminate Vivarium session?", func() tea.Cmd { return tea.Quit })
	}
	return a.delegate(msg)
}

func (a *App) delegate(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := a.top().Update(msg)
	a.stack[len(a.stack)-1] = next
	return a, cmd
}

func (a *App) top() Screen { return a.stack[len(a.stack)-1] }

// View implements tea.Model.
func (a *App) View() string {
	view := a.top().View()
	if a.confirm != nil {
		modal := a.theme.Modal.Render(a.confirm.prompt + "\n\n" +
			a.theme.Success.Render("[y]") + " Yes    " + a.theme.Error.Render("[n]") + " No")
		return lipgloss.Place(a.width, a.height, lipgloss.Center, lipgloss.Center, modal)
	}
	if a.status != "" {
		style := a.theme.Dim
		if a.statusErr {
			style = a.theme.Error
		}
		view += "\n" + style.Render(a.status)
	}
	if a.busy {
		label := a.busyLabel
		if label == "" {
			label = "Working"
		}
		view += "\n" + a.theme.FieldFocus.Render(a.spinner.View()+" "+label+"…")
	}
	return view
}

// helpBar renders a consistent footer of key hints.
func (a *App) helpBar(hints ...string) string {
	return a.theme.Dim.Render(strings.Join(hints, "   "))
}

package tui

import tea "github.com/charmbracelet/bubbletea"

// Screen is a single page in the navigation stack.
type Screen interface {
	ID() string
	Title() string
	Init() tea.Cmd
	Update(tea.Msg) (Screen, tea.Cmd)
	View() string
}

// inputCapturer is implemented by screens that own raw keyboard input (for
// example a text field in edit mode). While capturing, global shortcuts are
// not intercepted.
type inputCapturer interface {
	CapturingInput() bool
}

// reloader is implemented by list screens so the App can refresh them when a
// child screen is popped (for example after saving a new item).
type reloader interface {
	Reload() tea.Cmd
}

// Navigation and status messages.
type (
	pushMsg       struct{ screen Screen }
	popMsg        struct{}
	replaceMsg    struct{ screen Screen }
	resetStackMsg struct{ screen Screen }
	statusMsg     struct {
		text string
		err  bool
	}
	confirmMsg struct {
		prompt string
		onYes  func() tea.Cmd
	}
	busyMsg struct {
		on    bool
		label string
	}
)

// setBusy toggles the working indicator.
func setBusy(on bool, label string) tea.Cmd {
	return func() tea.Msg { return busyMsg{on: on, label: label} }
}

func push(s Screen) tea.Cmd    { return func() tea.Msg { return pushMsg{s} } }
func pop() tea.Cmd             { return func() tea.Msg { return popMsg{} } }
func replace(s Screen) tea.Cmd { return func() tea.Msg { return replaceMsg{s} } }
func resetTo(s Screen) tea.Cmd { return func() tea.Msg { return resetStackMsg{s} } }

func setStatus(text string, isErr bool) tea.Cmd {
	return func() tea.Msg { return statusMsg{text: text, err: isErr} }
}

func confirm(prompt string, onYes func() tea.Cmd) tea.Cmd {
	return func() tea.Msg { return confirmMsg{prompt: prompt, onYes: onYes} }
}

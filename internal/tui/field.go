package tui

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// field is a text input with navigation and edit modes per FRONTEND.md §3.3.
type field struct {
	label     string
	input     textinput.Model
	committed string
	editing   bool
	focused   bool
	theme     Theme
}

func newField(theme Theme, label, placeholder string, password bool) *field {
	in := textinput.New()
	in.Placeholder = placeholder
	if password {
		in.EchoMode = textinput.EchoPassword
	}
	return &field{label: label, input: in, theme: theme}
}

func (f *field) SetValue(v string) {
	f.committed = v
	f.input.SetValue(v)
}

func (f *field) Value() string { return f.input.Value() }

// Dirty reports whether the value differs from the last commit.
func (f *field) Dirty() bool { return f.input.Value() != f.committed }

func (f *field) begin() {
	f.editing = true
	f.input.Focus()
}

func (f *field) commit() {
	f.committed = f.input.Value()
	f.editing = false
	f.input.Blur()
}

func (f *field) cancel() {
	f.input.SetValue(f.committed)
	f.editing = false
	f.input.Blur()
}

func (f *field) setFocus(focused bool) {
	f.focused = focused
}

func (f *field) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	f.input, cmd = f.input.Update(msg)
	return cmd
}

func (f *field) view() string {
	labelStyle := f.theme.FieldLabel
	switch {
	case f.editing:
		labelStyle = f.theme.FieldFocus
	case f.focused:
		labelStyle = f.theme.FieldFocus
	}
	value := f.input.View()
	if !f.editing && !f.focused {
		value = f.theme.Dim.Render(f.input.Value())
		if f.input.Value() == "" {
			value = f.theme.Dim.Render(f.input.Placeholder)
		}
	}
	return labelStyle.Render(f.label) + ": " + value
}

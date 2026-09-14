package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// fieldSpec describes one text field in a fieldFormScreen.
type fieldSpec struct {
	name        string
	label       string
	placeholder string
	password    bool
}

// formResultMsg reports the outcome of an asynchronous form submission.
type formResultMsg struct{ err error }

// fieldFormScreen is a small vertical form of text fields plus Submit/Cancel
// buttons. Validation runs on submit; the submission is asynchronous and pops
// the screen on success.
type fieldFormScreen struct {
	app      *App
	title    string
	form     *form
	fields   map[string]*field
	validate func(map[string]string) string
	onSubmit func(map[string]string) tea.Cmd
	err      string
}

func newFieldFormScreen(app *App, title string, specs []fieldSpec, submitLabel string,
	validate func(map[string]string) string, onSubmit func(map[string]string) tea.Cmd) *fieldFormScreen {
	s := &fieldFormScreen{app: app, title: title, fields: map[string]*field{}, validate: validate, onSubmit: onSubmit}
	s.form = newForm(app.theme)
	for _, spec := range specs {
		f := newField(app.theme, spec.label, spec.placeholder, spec.password)
		s.fields[spec.name] = f
		s.form.addField(f)
	}
	s.form.addButton(
		button{Key: "submit", Label: submitLabel, Hotkey: "C-s"},
		button{Key: "cancel", Label: "Cancel", Hotkey: "Esc"},
	)
	return s
}

func (s *fieldFormScreen) ID() string           { return "field-form" }
func (s *fieldFormScreen) Title() string        { return s.title }
func (s *fieldFormScreen) CapturingInput() bool { return s.form.Capturing() }
func (s *fieldFormScreen) Init() tea.Cmd        { return nil }

// setValues pre-populates fields by name.
func (s *fieldFormScreen) setValues(values map[string]string) {
	for name, v := range values {
		if f, ok := s.fields[name]; ok {
			f.SetValue(v)
		}
	}
}

func (s *fieldFormScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	if result, ok := msg.(formResultMsg); ok {
		if result.err != nil {
			s.err = result.err.Error()
			return s, setBusy(false, "")
		}
		return s, pop()
	}
	if s.form.Capturing() {
		_, _, cmd := s.form.Update(msg)
		return s, cmd
	}
	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "esc" {
		return s, pop()
	}
	action, handled, cmd := s.form.Update(msg)
	if handled && action != "" {
		switch action {
		case "submit":
			values := map[string]string{}
			for name, f := range s.fields {
				values[name] = f.Value()
			}
			if s.validate != nil {
				if message := s.validate(values); message != "" {
					s.err = message
					return s, nil
				}
			}
			s.err = ""
			if s.onSubmit != nil {
				return s, s.onSubmit(values)
			}
			return s, pop()
		case "cancel":
			return s, pop()
		}
	}
	return s, cmd
}

func (s *fieldFormScreen) View() string {
	t := s.app.theme
	var b strings.Builder
	b.WriteString(t.Title.Render(s.title) + "\n\n")
	if s.err != "" {
		b.WriteString(t.Error.Render(s.err) + "\n\n")
	}
	s.form.setTop(2)
	b.WriteString(s.form.view())
	b.WriteString("\n\n" + t.Dim.Render("Enter edit/activate · ↑↓ move · Esc back"))
	return b.String()
}

package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// button is a selectable action rendered as a row.
type button struct {
	Key      string
	Label    string
	Hotkey   string
	Disabled bool
}

type rowKind int

const (
	rowField rowKind = iota
	rowAction
	rowButton
)

type formRow struct {
	kind     rowKind
	field    *field
	key      string
	label    string
	hotkey   string
	disabled bool
}

// form is a vertical list of text fields, drill-down actions, and buttons. A
// single cursor moves through every row with Up/Down/Tab; Enter edits a field
// or activates the focused row.
type form struct {
	rows      []formRow
	cursor    int
	theme     Theme
	top       int
	lastClick time.Time
	lastIndex int
}

func newForm(theme Theme) *form {
	return &form{theme: theme, lastIndex: -1}
}

// setTop records the screen line where the first row is rendered (for mouse).
func (f *form) setTop(top int) { f.top = top }

func (f *form) addField(field *field) {
	f.rows = append(f.rows, formRow{kind: rowField, field: field})
	f.sync()
}

func (f *form) addAction(key, label string) {
	f.rows = append(f.rows, formRow{kind: rowAction, key: key, label: label})
}

// addDisabledAction adds a non-selectable action row (rendered dim), used for
// choices that are fixed at creation time.
func (f *form) addDisabledAction(key, label string) {
	f.rows = append(f.rows, formRow{kind: rowAction, key: key, label: label, disabled: true})
}

// focusFirst moves the cursor to the first selectable row, if any.
func (f *form) focusFirst() {
	f.cursor = 0
	if !f.selectable(f.cursor) {
		f.move(1)
	}
	f.sync()
}

// setActionLabel updates the label of a previously added action row.
func (f *form) setActionLabel(key, label string) {
	for i := range f.rows {
		if f.rows[i].kind == rowAction && f.rows[i].key == key {
			f.rows[i].label = label
			return
		}
	}
}

func (f *form) addButton(buttons ...button) {
	for _, b := range buttons {
		f.rows = append(f.rows, formRow{
			kind: rowButton, key: b.Key, label: b.Label, hotkey: b.Hotkey, disabled: b.Disabled,
		})
	}
}

func (f *form) selectable(i int) bool {
	return i >= 0 && i < len(f.rows) && !f.rows[i].disabled
}

func (f *form) sync() {
	for i := range f.rows {
		if f.rows[i].kind == rowField {
			f.rows[i].field.setFocus(i == f.cursor)
		}
	}
}

func (f *form) move(delta int) {
	if len(f.rows) == 0 {
		return
	}
	i := f.cursor
	for {
		i = (i + delta + len(f.rows)) % len(f.rows)
		if f.selectable(i) {
			f.cursor = i
			f.sync()
			return
		}
		if i == f.cursor {
			return
		}
	}
}

// Capturing reports whether a field is in edit mode.
func (f *form) Capturing() bool { return f.editingField() != nil }

// Dirty reports whether any field has uncommitted changes.
func (f *form) Dirty() bool {
	for i := range f.rows {
		if f.rows[i].kind == rowField && f.rows[i].field.Dirty() {
			return true
		}
	}
	return false
}

func (f *form) editingField() *field {
	for i := range f.rows {
		if f.rows[i].kind == rowField && f.rows[i].field.editing {
			return f.rows[i].field
		}
	}
	return nil
}

// Update returns an activated action/button key, whether the message was
// handled, and any command.
func (f *form) Update(msg tea.Msg) (string, bool, tea.Cmd) {
	if field := f.editingField(); field != nil {
		if key, ok := msg.(tea.KeyMsg); ok {
			switch key.String() {
			case "enter":
				field.commit()
				return "", true, nil
			case "esc":
				field.cancel()
				return "", true, nil
			case "tab", "down":
				field.commit()
				f.move(1)
				return "", true, nil
			case "shift+tab", "up":
				field.commit()
				f.move(-1)
				return "", true, nil
			}
		}
		return "", true, field.update(msg)
	}
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k", "shift+tab":
			f.move(-1)
			return "", true, nil
		case "down", "j", "tab":
			f.move(1)
			return "", true, nil
		case "enter":
			return f.activate(f.cursor), true, nil
		}
	case tea.MouseMsg:
		return f.handleMouse(msg), true, nil
	}
	return "", false, nil
}

func (f *form) activate(i int) string {
	if !f.selectable(i) {
		return ""
	}
	row := &f.rows[i]
	switch row.kind {
	case rowField:
		row.field.begin()
		return ""
	case rowAction, rowButton:
		return row.key
	}
	return ""
}

func (f *form) handleMouse(mm tea.MouseMsg) string {
	if mm.Button != tea.MouseButtonLeft || mm.Action != tea.MouseActionPress {
		return ""
	}
	idx := mm.Y - f.top
	if idx < 0 || idx >= len(f.rows) || !f.selectable(idx) {
		return ""
	}
	if idx == f.lastIndex && time.Since(f.lastClick) < 400*time.Millisecond {
		f.lastClick = time.Time{}
		f.lastIndex = -1
		f.cursor = idx
		f.sync()
		return f.activate(idx)
	}
	f.cursor = idx
	f.sync()
	f.lastClick = time.Now()
	f.lastIndex = idx
	return ""
}

func (f *form) view() string {
	var b strings.Builder
	for i := range f.rows {
		b.WriteString(f.renderRow(i))
		if i < len(f.rows)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (f *form) renderRow(i int) string {
	row := f.rows[i]
	selected := i == f.cursor
	switch row.kind {
	case rowField:
		return row.field.view()
	case rowAction:
		if row.disabled {
			return f.theme.Dim.Render(row.label)
		}
		style := f.theme.FieldLabel
		if selected {
			style = f.theme.FieldFocus
		}
		return style.Render(row.label) + f.theme.Dim.Render(" ▸")
	case rowButton:
		label := row.label
		if row.hotkey != "" {
			label = "[" + row.hotkey + "] " + label
		}
		style := f.theme.Button
		switch {
		case row.disabled:
			style = f.theme.Dim
		case selected:
			style = f.theme.ButtonHot
		}
		return style.Render(label)
	}
	return ""
}

package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// menuItem is a single row in a menu.
type menuItem struct {
	Header     bool
	Key        string // action identifier
	Hotkey     string
	Label      string
	Detail     string
	Badge      string
	BadgeColor lipgloss.Color
	Disabled   bool
}

// menu is a scrollable, sectioned single-list selector.
type menu struct {
	items     []menuItem
	cursor    int
	offset    int
	height    int
	width     int
	top       int // screen line where the first item is rendered (for mouse)
	theme     Theme
	lastClick time.Time
	lastIndex int
}

func newMenu(theme Theme) *menu { return &menu{theme: theme, lastIndex: -1} }

// setTop records the screen line where the menu starts, enabling mouse hit
// testing.
func (m *menu) setTop(top int) { m.top = top }

func (m *menu) setItems(items []menuItem) {
	m.items = items
	m.cursor = 0
	// Start on the first selectable row, skipping any leading section headers.
	for i := range m.items {
		if m.selectable(i) {
			m.cursor = i
			break
		}
	}
	m.offset = 0
	m.ensureVisible()
}

func (m *menu) setSize(width, height int) {
	m.width = width
	m.height = height
	m.ensureVisible()
}

func (m *menu) selectable(i int) bool {
	return i >= 0 && i < len(m.items) && !m.items[i].Header && !m.items[i].Disabled
}

func (m *menu) move(delta int) {
	if len(m.items) == 0 {
		return
	}
	i := m.cursor
	for {
		i += delta
		if i < 0 || i >= len(m.items) {
			return
		}
		if m.selectable(i) {
			m.cursor = i
			m.ensureVisible()
			return
		}
	}
}

func (m *menu) toEdge(end bool) {
	if len(m.items) == 0 {
		return
	}
	if end {
		for i := len(m.items) - 1; i >= 0; i-- {
			if m.selectable(i) {
				m.cursor = i
				break
			}
		}
	} else {
		for i := 0; i < len(m.items); i++ {
			if m.selectable(i) {
				m.cursor = i
				break
			}
		}
	}
	m.ensureVisible()
}

func (m *menu) ensureVisible() {
	if m.height <= 0 || len(m.items) <= m.height {
		m.offset = 0
		return
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+m.height {
		m.offset = m.cursor - m.height + 1
	}
	max := len(m.items) - m.height
	if m.offset > max {
		m.offset = max
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

// selected returns the highlighted item's action key.
func (m *menu) selected() (string, bool) {
	if m.selectable(m.cursor) {
		return m.items[m.cursor].Key, true
	}
	return "", false
}

// update handles navigation and selection. It returns the action key of the
// chosen item when Enter is pressed.
func (m *menu) update(msg tea.Msg) (string, bool) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			m.move(-1)
		case "down", "j":
			m.move(1)
		case "home", "g":
			m.toEdge(false)
		case "end", "G":
			m.toEdge(true)
		case "pgup":
			m.move(-m.pageSize())
		case "pgdown":
			m.move(m.pageSize())
		case "enter":
			return m.selected()
		}
	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.move(-1)
		case tea.MouseButtonWheelDown:
			m.move(1)
		case tea.MouseButtonLeft:
			if msg.Action != tea.MouseActionPress {
				return "", false
			}
			idx := msg.Y - m.top + m.offset
			if idx < 0 || idx >= len(m.items) || !m.selectable(idx) {
				return "", false
			}
			if idx == m.lastIndex && time.Since(m.lastClick) < 400*time.Millisecond {
				m.lastClick = time.Time{}
				m.lastIndex = -1
				m.cursor = idx
				return m.selected()
			}
			m.cursor = idx
			m.lastClick = time.Now()
			m.lastIndex = idx
		}
	}
	return "", false
}

func (m *menu) pageSize() int {
	if m.height > 1 {
		return m.height - 1
	}
	return 1
}

func (m *menu) view() string {
	var b strings.Builder
	end := len(m.items)
	if m.height > 0 && m.offset+m.height < end {
		end = m.offset + m.height
	}
	for i := m.offset; i < end; i++ {
		item := m.items[i]
		switch {
		case item.Header:
			b.WriteString(m.theme.Header.Render(item.Label))
		case i == m.cursor && !item.Disabled:
			b.WriteString(m.theme.Selected.Render(m.renderRow(item)))
		case item.Disabled:
			b.WriteString(m.theme.Dim.Render(m.renderRow(item)))
		default:
			b.WriteString(m.renderRow(item))
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *menu) renderRow(item menuItem) string {
	var b strings.Builder
	b.WriteString("  ")
	if item.Hotkey != "" {
		b.WriteString("[" + item.Hotkey + "] ")
	}
	b.WriteString(item.Label)
	if item.Detail != "" {
		b.WriteString("  " + m.theme.Dim.Render(item.Detail))
	}
	row := b.String()
	if item.Badge != "" {
		badge := m.theme.Badge.Foreground(item.BadgeColor).Render(item.Badge)
		pad := m.width - lipgloss.Width(row) - lipgloss.Width(badge) - 2
		if pad < 1 {
			pad = 1
		}
		row += strings.Repeat(" ", pad) + badge
	}
	return row
}

// confirmState is a modal yes/no prompt.
type confirmState struct {
	prompt string
	onYes  func() tea.Cmd
}

// spinnerModel wraps the bubbles spinner with a label.
type spinnerModel struct {
	spinner spinner.Model
	label   string
}

func newSpinner() spinnerModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	return spinnerModel{spinner: s, label: "Working"}
}

func (s spinnerModel) View() string {
	return s.spinner.View() + " " + s.label + "…"
}

func (s spinnerModel) Update(msg tea.Msg) (spinnerModel, tea.Cmd) {
	var cmd tea.Cmd
	s.spinner, cmd = s.spinner.Update(msg)
	return s, cmd
}

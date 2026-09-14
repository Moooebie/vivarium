package tui

import (
	"bufio"
	"context"
	"io"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"vivarium/internal/apitypes"
	"vivarium/internal/models"
)

type baseImagesScreen struct {
	app  *App
	menu *menu
	imgs []apitypes.BaseImageView
	err  string
}

type baseImagesLoadedMsg struct {
	imgs []apitypes.BaseImageView
	err  error
}

type baseImagesActionMsg struct {
	status string
	err    error
}

func newBaseImagesScreen(app *App) *baseImagesScreen {
	return &baseImagesScreen{app: app, menu: newMenu(app.theme)}
}

func (s *baseImagesScreen) ID() string    { return "baseimages" }
func (s *baseImagesScreen) Title() string { return "Base Images" }
func (s *baseImagesScreen) Init() tea.Cmd { return s.load() }

// Reload implements reloader.
func (s *baseImagesScreen) Reload() tea.Cmd { return s.load() }

func (s *baseImagesScreen) load() tea.Cmd {
	return func() tea.Msg {
		imgs, err := s.app.ctx.Client.ListBaseImages(context.Background())
		return baseImagesLoadedMsg{imgs: imgs, err: err}
	}
}

func (s *baseImagesScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case baseImagesLoadedMsg:
		if msg.err != nil {
			s.err = msg.err.Error()
			return s, nil
		}
		s.err = ""
		s.imgs = msg.imgs
		items := make([]menuItem, 0, len(msg.imgs)+2)
		section := ""
		for _, img := range msg.imgs {
			sec := "custom"
			if img.IsInternal {
				sec = "standard"
			}
			if sec != section {
				label := "STANDARD PRESETS"
				if sec == "custom" {
					label = "CUSTOM IMAGES"
				}
				items = append(items, menuItem{Header: true, Label: label})
				section = sec
			}
			badge, color := "MISSING", lipgloss.Color("240")
			detail := string(img.SourceType) + "  ·  " + img.SourcePathOrRepo
			if img.Present {
				badge, color = "READY", lipgloss.Color("42")
				if img.SizeBytes > 0 {
					detail += "  ·  " + humanBytes(img.SizeBytes)
				}
			}
			items = append(items, menuItem{
				Key:        "image:" + img.ID,
				Label:      img.Name,
				Detail:     detail,
				Badge:      badge,
				BadgeColor: color,
			})
		}
		s.menu.setItems(items)
		s.menu.setSize(s.app.width, maxInt(1, s.app.height-6))
		return s, nil
	case baseImagesActionMsg:
		cmd := s.load()
		if msg.err != nil {
			return s, tea.Batch(cmd, setStatus(msg.status+": "+msg.err.Error(), true), setBusy(false, ""))
		}
		return s, tea.Batch(cmd, setStatus(msg.status, false), setBusy(false, ""))
	case tea.KeyMsg:
		return s.handleKey(msg)
	}
	return s, nil
}

func (s *baseImagesScreen) selected() (apitypes.BaseImageView, bool) {
	key, ok := s.menu.selected()
	if !ok || !strings.HasPrefix(key, "image:") {
		return apitypes.BaseImageView{}, false
	}
	id := strings.TrimPrefix(key, "image:")
	for _, img := range s.imgs {
		if img.ID == id {
			return img, true
		}
	}
	return apitypes.BaseImageView{}, false
}

func (s *baseImagesScreen) handleKey(msg tea.KeyMsg) (Screen, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return s, pop()
	case "r":
		return s, push(newLogScreen(s.app, "Rebuilding standard images", func(w io.Writer) error {
			return s.app.ctx.Client.RebuildBaseImages(context.Background(), "", w)
		}))
	case "n":
		return s, push(newBaseImageFormScreen(s.app))
	case "d":
		if img, ok := s.selected(); ok {
			if img.IsInternal {
				return s, setStatus("standard presets cannot be removed", true)
			}
			id, name := img.ID, img.Name
			return s, confirm("Remove image registration "+name+"?", func() tea.Cmd { return s.deleteImage(id) })
		}
	}
	if _, ok := s.menu.update(msg); ok {
		return s, nil
	}
	return s, nil
}

func (s *baseImagesScreen) deleteImage(id string) tea.Cmd {
	return func() tea.Msg {
		err := s.app.ctx.Client.DeleteBaseImage(context.Background(), id)
		return baseImagesActionMsg{status: "image removed", err: err}
	}
}

func (s *baseImagesScreen) View() string {
	t := s.app.theme
	var b strings.Builder
	b.WriteString(t.Title.Render("Base Images") + "\n\n")
	if s.err != "" {
		b.WriteString(t.Error.Render(s.err) + "\n\n")
	}
	s.menu.setTop(2)
	b.WriteString(s.menu.view())
	b.WriteString("\n\n" + s.app.helpBar("n register", "r rebuild presets", "d remove", "Esc back"))
	return b.String()
}

// ---- Custom image form ----

type baseImageFormScreen struct {
	app           *App
	form          *form
	name          *field
	path          *field
	source        models.SourceType
	initialSource models.SourceType
	picking       bool
	pickMenu      *menu
	err           string
}

type baseImageSavedMsg struct {
	img models.BaseImage
	err error
}

func newBaseImageFormScreen(app *App) *baseImageFormScreen {
	s := &baseImageFormScreen{app: app, source: models.SourceDockerHub}
	s.form = newForm(app.theme)
	s.name = newField(app.theme, "Name", "Input a name…", false)
	s.path = newField(app.theme, "Source", "owner/repo:tag or /path/to/Dockerfile", false)
	s.form.addField(s.name)
	s.form.addField(s.path)
	s.form.addAction("type", "Type")
	s.form.addButton(button{Key: "save", Label: "Register", Hotkey: "C-s"})
	s.form.setActionLabel("type", "Type: "+string(s.source))
	s.pickMenu = newMenu(app.theme)
	s.pickMenu.setItems([]menuItem{
		{Key: "dockerhub", Label: "Docker Hub", Detail: "owner/repo:tag"},
		{Key: "dockerfile", Label: "Local Dockerfile", Detail: "absolute host path"},
	})
	s.initialSource = s.source
	return s
}

func (s *baseImageFormScreen) ID() string           { return "baseimage-form" }
func (s *baseImageFormScreen) Title() string        { return "Register Image" }
func (s *baseImageFormScreen) CapturingInput() bool { return s.form.Capturing() || s.picking }
func (s *baseImageFormScreen) Init() tea.Cmd        { return nil }

func (s *baseImageFormScreen) dirty() bool {
	return s.form.Dirty() || s.source != s.initialSource
}

func (s *baseImageFormScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case baseImageSavedMsg:
		if msg.err != nil {
			s.err = msg.err.Error()
			return s, setBusy(false, "")
		}
		return s, tea.Batch(pop(), setStatus("registered "+msg.img.Name, false), setBusy(false, ""))
	case tea.KeyMsg:
		return s.handleKey(msg)
	}
	return s, nil
}

func (s *baseImageFormScreen) handleKey(msg tea.KeyMsg) (Screen, tea.Cmd) {
	if s.picking {
		if msg.String() == "esc" {
			s.picking = false
			return s, nil
		}
		if key, ok := s.pickMenu.update(msg); ok {
			s.source = models.SourceType(key)
			s.picking = false
			s.form.setActionLabel("type", "Type: "+string(s.source))
		}
		return s, nil
	}
	if s.form.Capturing() {
		_, _, cmd := s.form.Update(msg)
		return s, cmd
	}
	switch msg.String() {
	case "esc":
		if s.dirty() {
			return s, confirm("Discard all unsaved changes and go back?", func() tea.Cmd { return pop() })
		}
		return s, pop()
	case "ctrl+s":
		return s, s.save()
	}
	action, handled, cmd := s.form.Update(msg)
	if handled && action != "" {
		switch action {
		case "save":
			return s, s.save()
		case "type":
			s.picking = true
		}
	}
	return s, cmd
}

func (s *baseImageFormScreen) save() tea.Cmd {
	name := strings.TrimSpace(s.name.Value())
	pathValue := strings.TrimSpace(s.path.Value())
	if name == "" {
		s.err = "name is required"
		return nil
	}
	if pathValue == "" {
		s.err = "source is required"
		return nil
	}
	if s.source == models.SourceDockerfile {
		pathValue = expandPath(pathValue)
	}
	s.err = ""
	img := models.BaseImage{
		Name:             name,
		SourceType:       s.source,
		SourcePathOrRepo: pathValue,
		DefaultUser:      "root",
	}
	return tea.Batch(setBusy(true, "Registering image"), func() tea.Msg {
		out, err := s.app.ctx.Client.CreateBaseImage(context.Background(), img)
		return baseImageSavedMsg{img: out, err: err}
	})
}

func (s *baseImageFormScreen) View() string {
	t := s.app.theme
	var b strings.Builder
	b.WriteString(t.Title.Render("Register Custom Image") + "\n\n")
	if s.picking {
		b.WriteString("Source type:\n\n" + s.pickMenu.view())
		return b.String()
	}
	s.form.setTop(2)
	b.WriteString(s.form.view() + "\n")
	if s.err != "" {
		b.WriteString("\n" + t.Error.Render(s.err) + "\n")
	}
	b.WriteString("\n" + t.Dim.Render("Enter edit/activate · ↑↓ move · Esc back"))
	return b.String()
}

// ---- Streaming log screen ----

type logScreen struct {
	app     *App
	title   string
	start   func(io.Writer) error
	lines   []string
	scanner *bufio.Scanner
	done    bool
	err     error
}

type logLineMsg struct{ line string }
type logDoneMsg struct{ err error }

func newLogScreen(app *App, title string, start func(io.Writer) error) *logScreen {
	return &logScreen{app: app, title: title, start: start}
}

func (s *logScreen) ID() string    { return "log" }
func (s *logScreen) Title() string { return s.title }

func (s *logScreen) Init() tea.Cmd {
	pr, pw := io.Pipe()
	s.scanner = bufio.NewScanner(pr)
	go func() {
		err := s.start(pw)
		_ = pw.CloseWithError(err)
	}()
	return s.wait()
}

func (s *logScreen) wait() tea.Cmd {
	scanner := s.scanner
	return func() tea.Msg {
		if scanner.Scan() {
			return logLineMsg{line: scanner.Text()}
		}
		if err := scanner.Err(); err != nil {
			return logDoneMsg{err: err}
		}
		return logDoneMsg{}
	}
}

func (s *logScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case logLineMsg:
		s.lines = append(s.lines, msg.line)
		if len(s.lines) > 500 {
			s.lines = s.lines[len(s.lines)-500:]
		}
		return s, s.wait()
	case logDoneMsg:
		s.done = true
		s.err = msg.err
		return s, nil
	case tea.KeyMsg:
		if msg.String() == "esc" || msg.String() == "enter" || msg.String() == "q" {
			return s, pop()
		}
	}
	return s, nil
}

func (s *logScreen) View() string {
	t := s.app.theme
	var b strings.Builder
	b.WriteString(t.Title.Render(s.title) + "\n\n")
	visible := s.lines
	if len(visible) > 20 {
		visible = visible[len(visible)-20:]
	}
	for _, line := range visible {
		b.WriteString(truncate(line, s.app.width) + "\n")
	}
	if s.done {
		if s.err != nil {
			b.WriteString("\n" + t.Error.Render("failed: "+s.err.Error()) + "\n")
		} else {
			b.WriteString("\n" + t.Success.Render("done") + "\n")
		}
	} else {
		b.WriteString("\n" + t.FieldFocus.Render(s.app.spinner.View()+" building…") + "\n")
	}
	b.WriteString("\n" + t.Dim.Render("Esc close"))
	return b.String()
}

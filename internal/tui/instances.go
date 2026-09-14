package tui

import (
	"context"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"vivarium/internal/apitypes"
	"vivarium/internal/models"
)

type instanceRow struct {
	view   apitypes.InstanceView
	pinned bool
}

type instancesScreen struct {
	app  *App
	menu *menu
	rows []instanceRow
	err  string
}

type instancesLoadedMsg struct {
	views []apitypes.InstanceView
	err   error
}

type instancesActionMsg struct {
	status string
	err    error
}

func newInstancesScreen(app *App) *instancesScreen {
	return &instancesScreen{app: app, menu: newMenu(app.theme)}
}

func (s *instancesScreen) ID() string    { return "instances" }
func (s *instancesScreen) Title() string { return "Instances" }

func (s *instancesScreen) Init() tea.Cmd { return s.load() }

// Reload implements reloader.
func (s *instancesScreen) Reload() tea.Cmd { return s.load() }

func (s *instancesScreen) load() tea.Cmd {
	return func() tea.Msg {
		views, err := s.app.ctx.Client.ListInstances(context.Background())
		return instancesLoadedMsg{views: views, err: err}
	}
}

func (s *instancesScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case instancesLoadedMsg:
		if msg.err != nil {
			s.err = msg.err.Error()
			return s, nil
		}
		s.err = ""
		s.setViews(msg.views)
		return s, nil
	case instancesActionMsg:
		cmd := s.load()
		if msg.err != nil {
			return s, tea.Batch(cmd, setStatus(msg.status+": "+msg.err.Error(), true), setBusy(false, ""))
		}
		return s, tea.Batch(cmd, setStatus(msg.status, false), setBusy(false, ""))
	case connectDoneMsg:
		if msg.err != nil {
			return s, tea.Batch(s.load(), setStatus("connect: "+msg.err.Error(), true))
		}
		return s, tea.Batch(s.load(), setStatus("shell exited", false))
	case tea.KeyMsg:
		return s.handleKey(msg)
	}
	return s, nil
}

func (s *instancesScreen) handleKey(msg tea.KeyMsg) (Screen, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return s, pop()
	case "n":
		return s, push(newWizardScreen(s.app))
	case "r":
		return s, s.load()
	case "e", "enter":
		if v, ok := s.selected(); ok {
			return s, push(newEditInstanceScreen(s.app, v))
		}
	case "a":
		if v, ok := s.selected(); ok {
			if v.Status != models.StatusRunning {
				return s, setStatus("instance is halted — press s to start", true)
			}
			cmd := &connectCmd{client: s.app.ctx.Client, id: v.ID, ctx: context.Background()}
			return s, tea.Exec(cmd, func(err error) tea.Msg { return connectDoneMsg{err: err} })
		}
	case "s":
		if v, ok := s.selected(); ok {
			return s, s.toggle(v)
		}
	case "d":
		if v, ok := s.selected(); ok {
			id := v.ID
			name := v.Name
			return s, confirm("Delete instance "+name+"? This removes the container.", func() tea.Cmd {
				return s.deleteInstance(id)
			})
		}
	case "c":
		if v, ok := s.selected(); ok {
			return s, s.clone(v)
		}
	case "k":
		if v, ok := s.selected(); ok {
			return s, push(newConnectionInfoScreen(s.app, v))
		}
	}
	if _, ok := s.menu.update(msg); ok {
		if v, ok := s.selected(); ok {
			return s, push(newEditInstanceScreen(s.app, v))
		}
	}
	return s, nil
}

func (s *instancesScreen) selected() (apitypes.InstanceView, bool) {
	key, ok := s.menu.selected()
	if !ok || !strings.HasPrefix(key, "instance:") {
		return apitypes.InstanceView{}, false
	}
	id := strings.TrimPrefix(key, "instance:")
	for _, r := range s.rows {
		if r.view.ID == id {
			return r.view, true
		}
	}
	return apitypes.InstanceView{}, false
}

func (s *instancesScreen) toggle(v apitypes.InstanceView) tea.Cmd {
	label := "Starting " + v.Name
	if v.Status == models.StatusRunning {
		label = "Halting " + v.Name
	}
	return tea.Batch(setBusy(true, label), func() tea.Msg {
		var err error
		action := "halted"
		if v.Status == models.StatusRunning {
			_, err = s.app.ctx.Client.HaltInstance(context.Background(), v.ID)
		} else {
			action = "started"
			_, err = s.app.ctx.Client.StartInstance(context.Background(), v.ID)
		}
		return instancesActionMsg{status: v.Name + " " + action, err: err}
	})
}

func (s *instancesScreen) deleteInstance(id string) tea.Cmd {
	return tea.Batch(setBusy(true, "Deleting instance"), func() tea.Msg {
		err := s.app.ctx.Client.DeleteInstance(context.Background(), id)
		return instancesActionMsg{status: "instance deleted", err: err}
	})
}

func (s *instancesScreen) clone(v apitypes.InstanceView) tea.Cmd {
	req := cloneRequest(v)
	return tea.Batch(setBusy(true, "Cloning "+v.Name), func() tea.Msg {
		_, err := s.app.ctx.Client.CreateInstance(context.Background(), req)
		return instancesActionMsg{status: "cloned " + v.Name, err: err}
	})
}

func cloneRequest(v apitypes.InstanceView) apitypes.InstanceCreateRequest {
	eps := make([]models.APIKey, 0, len(v.Endpoints))
	for _, e := range v.Endpoints {
		eps = append(eps, models.APIKey{
			ID: e.KeyID, Name: e.KeyID, ProviderType: e.ProviderType,
			BaseURL: e.BaseURL, MockURL: e.MockURL, RateLimitRPM: e.RateLimitRPM, CreatedAt: 1,
		})
	}
	return apitypes.InstanceCreateRequest{
		Name:         v.Name + "-copy",
		BaseImageTag: v.BaseImageTag,
		Recipe: &models.Recipe{
			Name:          v.Name + " copy",
			APIEndpoints:  eps,
			Resources:     v.Resources,
			DefaultMounts: v.Mounts,
			GPUs:          v.GPUs,
		},
	}
}

func (s *instancesScreen) setViews(views []apitypes.InstanceView) {
	rows := make([]instanceRow, 0, len(views))
	for _, v := range views {
		pinned := false
		for _, m := range v.Mounts {
			if m.HostPath == s.app.ctx.Cwd {
				pinned = true
				break
			}
		}
		rows = append(rows, instanceRow{view: v, pinned: pinned})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.pinned != b.pinned {
			return a.pinned
		}
		ar, br := a.view.Status == models.StatusRunning, b.view.Status == models.StatusRunning
		if ar != br {
			return ar
		}
		return a.view.Name < b.view.Name
	})
	s.rows = rows

	items := make([]menuItem, 0, len(rows)+3)
	section := ""
	for _, r := range rows {
		sec := "halted"
		switch {
		case r.pinned:
			sec = "pinned"
		case r.view.Status == models.StatusRunning:
			sec = "running"
		}
		if sec != section {
			labels := map[string]string{
				"pinned":  "PINNED — CURRENT DIRECTORY MOUNTED",
				"running": "RUNNING",
				"halted":  "HALTED",
			}
			items = append(items, menuItem{Header: true, Label: labels[sec]})
			section = sec
		}
		detail := r.view.BaseImageTag + "  ·  " + formatAgo(r.view.LastRunAt)
		if r.view.DiskUsageBytes > 0 {
			detail += "  ·  " + humanBytes(r.view.DiskUsageBytes)
		}
		items = append(items, menuItem{
			Key:        "instance:" + r.view.ID,
			Label:      r.view.Name,
			Detail:     detail,
			Badge:      strings.ToUpper(string(r.view.Status)),
			BadgeColor: badgeColor(r.view.Status),
		})
	}
	if len(rows) == 0 {
		items = append(items, menuItem{Header: true, Label: "NO INSTANCES — press n to create one"})
	}
	s.menu.setItems(items)
	s.menu.setSize(s.app.width, maxInt(1, s.app.height-6))
}

func badgeColor(status models.InstanceStatus) lipgloss.Color {
	switch status {
	case models.StatusRunning:
		return lipgloss.Color("42")
	case models.StatusBuilding:
		return lipgloss.Color("214")
	case models.StatusError:
		return lipgloss.Color("203")
	default:
		return lipgloss.Color("240")
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (s *instancesScreen) View() string {
	t := s.app.theme
	var b strings.Builder
	b.WriteString(t.Title.Render("Instances") + "\n\n")
	if s.err != "" {
		b.WriteString(t.Error.Render(s.err) + "\n\n")
	}
	b.WriteString(s.menu.view())
	b.WriteString("\n\n" + s.app.helpBar(
		"n new", "e edit", "s start/halt", "a attach", "c clone", "k info", "d delete", "r refresh", "Esc back"))
	return b.String()
}

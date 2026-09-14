package tui

import (
	"context"
	"errors"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"vivarium/internal/apitypes"
	"vivarium/internal/models"
)

type editInstanceScreen struct {
	app  *App
	inst apitypes.InstanceView
	form *form
	err  string
}

type editDoneMsg struct {
	inst   apitypes.InstanceView
	status string
	err    error
}

// editActionResultMsg reports a delete/clone; leave=true pops back to the list.
type editActionResultMsg struct {
	status string
	err    error
	leave  bool
}

func newEditInstanceScreen(app *App, inst apitypes.InstanceView) *editInstanceScreen {
	s := &editInstanceScreen{app: app, inst: inst}
	s.form = newForm(app.theme)
	s.form.addAction("endpoints", "API Endpoints")
	s.form.addDisabledAction("mounts", "Mount Points (fixed at creation)")
	s.form.addDisabledAction("gpus", "GPUs (fixed at creation)")
	s.form.addAction("info", "Connection Info")
	s.form.addAction("toggle", "Start/Halt")
	s.form.addAction("connect", "Connect Shell")
	s.form.addAction("clone", "Clone")
	s.form.addAction("rename", "Rename")
	s.form.addAction("inject", "Inject File")
	s.form.addButton(button{Key: "delete", Label: "Delete", Hotkey: "C-d"})
	s.refreshLabels()
	s.form.focusFirst()
	return s
}

func (s *editInstanceScreen) ID() string           { return "instance-edit" }
func (s *editInstanceScreen) Title() string        { return "Edit Instance" }
func (s *editInstanceScreen) CapturingInput() bool { return s.form.Capturing() }

func (s *editInstanceScreen) refreshLabels() {
	s.form.setActionLabel("endpoints", "API Endpoints: "+itoa(len(s.inst.Endpoints)))
}

func (s *editInstanceScreen) Init() tea.Cmd { return nil }

// Reload re-fetches the instance; used when a child editor pops.
func (s *editInstanceScreen) Reload() tea.Cmd {
	return func() tea.Msg {
		view, err := s.app.ctx.Client.GetInstance(context.Background(), s.inst.ID)
		return editDoneMsg{inst: view, err: err}
	}
}

func (s *editInstanceScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case editDoneMsg:
		if msg.err != nil {
			s.err = msg.err.Error()
			return s, setBusy(false, "")
		}
		if msg.inst.ID != "" {
			s.inst = msg.inst
			s.refreshLabels()
		}
		if msg.status != "" {
			return s, tea.Batch(setStatus(msg.status, false), setBusy(false, ""))
		}
		return s, setBusy(false, "")
	case editActionResultMsg:
		if msg.err != nil {
			s.err = msg.err.Error()
			return s, setBusy(false, "")
		}
		if msg.leave {
			return s, tea.Batch(pop(), setStatus(msg.status, false), setBusy(false, ""))
		}
		return s, tea.Batch(setStatus(msg.status, false), setBusy(false, ""))
	case connectDoneMsg:
		if msg.err != nil {
			return s, setStatus("connect: "+msg.err.Error(), true)
		}
		return s, setStatus("shell exited", false)
	case tea.KeyMsg:
		return s.handleKey(msg)
	}
	return s, nil
}

func (s *editInstanceScreen) handleKey(msg tea.KeyMsg) (Screen, tea.Cmd) {
	if s.form.Capturing() {
		_, _, cmd := s.form.Update(msg)
		return s, cmd
	}
	switch msg.String() {
	case "esc":
		return s, pop()
	case "ctrl+d":
		return s, s.dispatch("delete")
	}
	action, handled, cmd := s.form.Update(msg)
	if handled && action != "" {
		return s, s.dispatch(action)
	}
	return s, cmd
}

func (s *editInstanceScreen) dispatch(action string) tea.Cmd {
	switch action {
	case "endpoints":
		return s.openEndpoints()
	case "info":
		return push(newConnectionInfoScreen(s.app, s.inst))
	case "toggle":
		return s.toggle()
	case "connect":
		if s.inst.Status != models.StatusRunning {
			return setStatus("instance is halted — press s to start", true)
		}
		cmd := &connectCmd{client: s.app.ctx.Client, id: s.inst.ID, ctx: context.Background()}
		return tea.Exec(cmd, func(err error) tea.Msg { return connectDoneMsg{err: err} })
	case "clone":
		return s.clone()
	case "rename":
		return s.rename()
	case "inject":
		return s.inject()
	case "delete":
		id, name := s.inst.ID, s.inst.Name
		return confirm("Delete instance "+name+"?", func() tea.Cmd {
			return tea.Batch(setBusy(true, "Deleting instance"), func() tea.Msg {
				err := s.app.ctx.Client.DeleteInstance(context.Background(), id)
				return editActionResultMsg{status: "instance deleted", err: err, leave: true}
			})
		})
	}
	return nil
}

func (s *editInstanceScreen) rename() tea.Cmd {
	id := s.inst.ID
	form := newFieldFormScreen(s.app, "Rename Instance",
		[]fieldSpec{{name: "name", label: "New Name", placeholder: s.inst.Name}},
		"Rename",
		func(v map[string]string) string {
			if strings.TrimSpace(v["name"]) == "" {
				return "name must not be empty"
			}
			return ""
		},
		func(v map[string]string) tea.Cmd {
			name := strings.TrimSpace(v["name"])
			return tea.Batch(setBusy(true, "Renaming instance"), func() tea.Msg {
				_, err := s.app.ctx.Client.UpdateInstance(context.Background(), id,
					apitypes.InstanceUpdateRequest{Name: name})
				return formResultMsg{err: err}
			})
		})
	form.setValues(map[string]string{"name": s.inst.Name})
	return push(form)
}

func (s *editInstanceScreen) inject() tea.Cmd {
	id := s.inst.ID
	form := newFieldFormScreen(s.app, "Inject File",
		[]fieldSpec{
			{name: "host", label: "Host File", placeholder: "/host/file"},
			{name: "guest", label: "Guest Path", placeholder: "/guest/path"},
		},
		"Inject",
		func(v map[string]string) string {
			if strings.TrimSpace(v["host"]) == "" || strings.TrimSpace(v["guest"]) == "" {
				return "host and guest paths are required"
			}
			return ""
		},
		func(v map[string]string) tea.Cmd {
			host := expandPath(v["host"])
			guest := strings.TrimSpace(v["guest"])
			return tea.Batch(setBusy(true, "Injecting file"), func() tea.Msg {
				err := s.app.ctx.Client.InjectInstance(context.Background(), id, apitypes.InjectRequest{
					HostSourcePath: host, GuestTargetPath: guest, FileMode: "0644",
				})
				return formResultMsg{err: err}
			})
		})
	return push(form)
}

func (s *editInstanceScreen) toggle() tea.Cmd {
	id := s.inst.ID
	halt := s.inst.Status == models.StatusRunning
	label := "Starting instance"
	if halt {
		label = "Halting instance"
	}
	return tea.Batch(setBusy(true, label), func() tea.Msg {
		ctx := context.Background()
		var out apitypes.InstanceView
		var err error
		if halt {
			out, err = s.app.ctx.Client.HaltInstance(ctx, id)
		} else {
			out, err = s.app.ctx.Client.StartInstance(ctx, id)
		}
		action := "started"
		if halt {
			action = "halted"
		}
		return editDoneMsg{inst: out, status: action, err: err}
	})
}

// openEndpoints opens the shared endpoints editor against a live instance.
// Edits are staged and applied once when the editor is left.
func (s *editInstanceScreen) openEndpoints() tea.Cmd {
	host := newInstanceEndpointsHost(s.inst)
	return push(newEndpointsEditorScreen(s.app, host, s.loadEndpointKeys, s.commitEndpoints(host)))
}

func (s *editInstanceScreen) loadEndpointKeys() tea.Cmd {
	return func() tea.Msg {
		keys, err := s.app.ctx.Client.ListAPIKeys(context.Background())
		return endpointsDataMsg{keys: keys, err: err}
	}
}

func (s *editInstanceScreen) commitEndpoints(host *instanceEndpointsHost) func() tea.Cmd {
	return func() tea.Cmd {
		if !host.dirty {
			return nil
		}
		ids := host.endpointIDs()
		return tea.Batch(setBusy(true, "Updating endpoints"), func() tea.Msg {
			view, err := s.app.ctx.Client.UpdateInstance(context.Background(), s.inst.ID,
				apitypes.InstanceUpdateRequest{EndpointKeys: &ids})
			return editDoneMsg{inst: view, status: "endpoints updated", err: err}
		})
	}
}

// instanceEndpointsHost adapts a live instance's bound endpoints to the shared
// endpoints editor. Changes are staged and committed once by the parent.
type instanceEndpointsHost struct {
	endpoints []models.APIKey
	saved     []apitypes.APIKeyView
	dirty     bool
}

func newInstanceEndpointsHost(inst apitypes.InstanceView) *instanceEndpointsHost {
	h := &instanceEndpointsHost{}
	for _, e := range inst.Endpoints {
		h.endpoints = append(h.endpoints, models.APIKey{
			ID: e.KeyID, Name: e.KeyID, ProviderType: e.ProviderType,
			BaseURL: e.BaseURL, MockURL: e.MockURL, RateLimitRPM: e.RateLimitRPM,
		})
	}
	return h
}

func (h *instanceEndpointsHost) endpointIDs() []string {
	ids := make([]string, 0, len(h.endpoints))
	for _, e := range h.endpoints {
		ids = append(ids, e.ID)
	}
	return ids
}

func (h *instanceEndpointsHost) Endpoints() []models.APIKey { return h.endpoints }
func (h *instanceEndpointsHost) SetEndpoints(eps []models.APIKey) {
	h.endpoints = eps
}
func (h *instanceEndpointsHost) SavedKeys() []apitypes.APIKeyView { return h.saved }
func (h *instanceEndpointsHost) SetSavedKeys(keys []apitypes.APIKeyView) {
	h.saved = keys
	byID := make(map[string]apitypes.APIKeyView, len(keys))
	for _, k := range keys {
		byID[k.ID] = k
	}
	for i := range h.endpoints {
		if k, ok := byID[h.endpoints[i].ID]; ok {
			h.endpoints[i].Name = k.Name
			h.endpoints[i].ProviderType = k.ProviderType
			h.endpoints[i].BaseURL = k.BaseURL
			h.endpoints[i].MockURL = k.MockURL
		}
	}
}
func (h *instanceEndpointsHost) AddEndpoint(k models.APIKey) error {
	for _, e := range h.endpoints {
		if e.ID == k.ID {
			return errors.New("that API key is already bound")
		}
	}
	h.endpoints = append(h.endpoints, k)
	return nil
}
func (h *instanceEndpointsHost) OnChanged() { h.dirty = true }

func (s *editInstanceScreen) clone() tea.Cmd {
	req := cloneRequest(s.inst)
	return tea.Batch(setBusy(true, "Cloning "+s.inst.Name), func() tea.Msg {
		_, err := s.app.ctx.Client.CreateInstance(context.Background(), req)
		return editActionResultMsg{status: "cloned " + s.inst.Name, err: err, leave: true}
	})
}

func (s *editInstanceScreen) View() string {
	t := s.app.theme
	var b strings.Builder
	b.WriteString(t.Title.Render("Edit Instance — "+s.inst.Name) + "\n\n")
	b.WriteString(t.Subtitle.Render("container "+s.inst.ContainerID+
		"   ip "+s.inst.IPAddress) + "   " + t.StatusBadge(string(s.inst.Status)) + "\n\n")
	if s.err != "" {
		b.WriteString(t.Error.Render(s.err) + "\n\n")
	}
	s.form.setTop(4)
	b.WriteString(s.form.view())
	b.WriteString("\n\n" + s.app.helpBar("Enter open/activate", "↑↓ move", "Esc back"))
	return b.String()
}

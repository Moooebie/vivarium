package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"vivarium/internal/docker"
	"vivarium/internal/guestbridge"
	"vivarium/internal/models"
)

func (s *Server) handleListInstances(w http.ResponseWriter, r *http.Request) {
	instances, err := s.store.ListInstances()
	if err != nil {
		writeMappedError(w, err)
		return
	}
	ctx := r.Context()
	views := make([]instanceView, 0, len(instances))
	for _, inst := range instances {
		views = append(views, s.reconcile(ctx, inst))
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *Server) handleGetInstance(w http.ResponseWriter, r *http.Request) {
	inst, err := s.store.GetInstance(r.PathValue("id"))
	if err != nil {
		writeMappedError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.reconcile(r.Context(), inst))
}

// reconcile refreshes an instance's live status and disk usage from Docker.
func (s *Server) reconcile(ctx context.Context, inst models.Instance) instanceView {
	view := instanceView{Instance: inst}
	if inst.ContainerID == "" {
		return view
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if status, err := s.docker.Status(ctx, inst.ContainerID); err == nil && status != inst.Status {
		inst.Status = status
		_ = s.store.PutInstance(inst)
		view.Instance = inst
	}
	if usage, err := s.docker.DiskUsage(ctx, inst.ContainerID); err == nil {
		view.DiskUsageBytes = usage
	}
	return view
}

func (s *Server) handleCreateInstance(w http.ResponseWriter, r *http.Request) {
	var req instanceCreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	recipe, err := s.resolveRecipe(req)
	if err != nil {
		writeMappedError(w, err)
		return
	}

	var baseImage models.BaseImage
	if recipe.BaseImageID != "" {
		baseImage, err = s.store.GetBaseImage(recipe.BaseImageID)
		if err != nil {
			writeMappedError(w, err)
			return
		}
	}

	imageTag := req.BaseImageTag
	if imageTag == "" {
		imageTag = baseImage.SourcePathOrRepo
	}
	if imageTag == "" {
		writeError(w, http.StatusBadRequest, "unable to determine a base image tag")
		return
	}

	name := req.Name
	if name == "" {
		name = "instance-" + randomHex(4)
	}

	mounts := make([]models.Mount, 0, len(recipe.DefaultMounts)+len(req.Mounts))
	mounts = append(mounts, recipe.DefaultMounts...)
	mounts = append(mounts, req.Mounts...)

	gpus := req.GPUs
	if len(gpus) == 0 {
		gpus = recipe.GPUs
	}

	endpoints := buildEndpoints(recipe.APIEndpoints)
	if err := s.verifyEndpointSecrets(endpoints); err != nil {
		writeError(w, http.StatusPreconditionFailed, err.Error())
		return
	}

	env := make(map[string]string, len(recipe.EnvVars))
	for k, v := range recipe.EnvVars {
		env[k] = v
	}
	for k, v := range providerKeyEnv(endpoints) {
		env[k] = v
	}
	if len(s.proxy.CACertPEM) > 0 {
		env["SSL_CERT_FILE"] = docker.GuestCAPEMPath
		env["REQUESTS_CA_BUNDLE"] = docker.GuestCAPEMPath
		env["NODE_EXTRA_CA_CERTS"] = docker.GuestCAPEMPath
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	if _, err := s.docker.EnsureNetwork(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "docker network: "+err.Error())
		return
	}

	bridge, err := s.guestBridgeSpec()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	mockHostIP := ""
	if bridge != nil {
		mockHostIP = "127.0.0.1"
	}

	user := baseImage.DefaultUser
	if user == "" {
		user = "root"
	}
	spec := docker.ContainerSpec{
		Name:        docker.SanitizeContainerName(name),
		Image:       imageTag,
		User:        user,
		Env:         env,
		Mounts:      mounts,
		GPUs:        gpus,
		MockHosts:   mockHosts(endpoints),
		MockHostIP:  mockHostIP,
		Resources:   recipe.Resources,
		CACert:      s.proxy.CACertPEM,
		GuestBridge: bridge,
	}
	containerID, err := s.docker.CreateAndStart(ctx, spec)
	if err != nil {
		writeError(w, http.StatusBadGateway, "create container: "+err.Error())
		return
	}
	ip, _ := s.docker.IPAddress(ctx, containerID)

	now := time.Now().Unix()
	inst := models.Instance{
		ID:           models.NewID(),
		ContainerID:  containerID,
		Name:         name,
		RecipeID:     req.RecipeID,
		Status:       models.StatusRunning,
		CreatedAt:    now,
		LastRunAt:    now,
		BaseImageTag: imageTag,
		Mounts:       mounts,
		GPUs:         gpus,
		IPAddress:    ip,
		EnvVars:      env,
		Endpoints:    endpoints,
		Resources:    recipe.Resources,
		User:         user,
	}
	if err := s.store.PutInstance(inst); err != nil {
		writeMappedError(w, err)
		return
	}
	s.register(inst)
	writeJSON(w, http.StatusCreated, instanceView{Instance: inst})
}

func (s *Server) handleUpdateInstance(w http.ResponseWriter, r *http.Request) {
	inst, err := s.store.GetInstance(r.PathValue("id"))
	if err != nil {
		writeMappedError(w, err)
		return
	}
	var req instanceUpdateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	structural := false
	if req.Name != "" {
		inst.Name = req.Name
	}
	if req.Mounts != nil {
		for _, m := range *req.Mounts {
			if err := m.Validate(); err != nil {
				writeMappedError(w, err)
				return
			}
		}
		inst.Mounts = *req.Mounts
		structural = true
	}
	if req.GPUs != nil {
		for _, g := range *req.GPUs {
			if err := g.Validate(); err != nil {
				writeMappedError(w, err)
				return
			}
		}
		inst.GPUs = *req.GPUs
		structural = true
	}

	if structural && inst.ContainerID != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
		defer cancel()
		if _, err := s.docker.EnsureNetwork(ctx); err != nil {
			writeError(w, http.StatusServiceUnavailable, "docker network: "+err.Error())
			return
		}
		recreated, err := s.recreateInstance(ctx, inst)
		if err != nil {
			writeError(w, http.StatusBadGateway, "recreate container: "+err.Error())
			return
		}
		inst = recreated
	}

	if err := s.store.PutInstance(inst); err != nil {
		writeMappedError(w, err)
		return
	}
	s.register(inst)
	writeJSON(w, http.StatusOK, instanceView{Instance: inst})
}

// recreateInstance replaces a container with one built from the instance's
// persisted configuration.
func (s *Server) recreateInstance(ctx context.Context, inst models.Instance) (models.Instance, error) {
	if inst.ContainerID != "" {
		_ = s.docker.Remove(ctx, inst.ContainerID)
	}
	user := inst.User
	if user == "" {
		user = "root"
	}
	bridge, err := s.guestBridgeSpec()
	if err != nil {
		return inst, err
	}
	mockHostIP := ""
	if bridge != nil {
		mockHostIP = "127.0.0.1"
	}
	spec := docker.ContainerSpec{
		Name:        docker.SanitizeContainerName(inst.Name),
		Image:       inst.BaseImageTag,
		User:        user,
		Env:         inst.EnvVars,
		Mounts:      inst.Mounts,
		GPUs:        inst.GPUs,
		MockHosts:   mockHosts(inst.Endpoints),
		MockHostIP:  mockHostIP,
		Resources:   inst.Resources,
		CACert:      s.proxy.CACertPEM,
		GuestBridge: bridge,
	}
	containerID, err := s.docker.CreateAndStart(ctx, spec)
	if err != nil {
		return inst, err
	}
	inst.ContainerID = containerID
	inst.Status = models.StatusRunning
	inst.LastRunAt = time.Now().Unix()
	if ip, err := s.docker.IPAddress(ctx, containerID); err == nil && ip != "" {
		inst.IPAddress = ip
	}
	return inst, nil
}

func (s *Server) resolveRecipe(req instanceCreateRequest) (models.Recipe, error) {
	if req.Recipe != nil {
		return *req.Recipe, nil
	}
	if req.RecipeID != nil && *req.RecipeID != "" {
		return s.store.GetRecipe(*req.RecipeID)
	}
	return models.Recipe{}, nil
}

func (s *Server) handleStartInstance(w http.ResponseWriter, r *http.Request) {
	inst, err := s.store.GetInstance(r.PathValue("id"))
	if err != nil {
		writeMappedError(w, err)
		return
	}
	if inst.ContainerID == "" {
		writeError(w, http.StatusConflict, "instance has no materialised container")
		return
	}
	ctx := r.Context()
	if err := s.docker.Client().StartContainer(ctx, inst.ContainerID); err != nil {
		writeError(w, http.StatusBadGateway, "start container: "+err.Error())
		return
	}
	bridge, err := s.guestBridgeSpec()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if bridge != nil {
		// Detached exec processes do not survive a stop/start.
		if err := s.docker.StartGuestBridge(ctx, inst.ContainerID, *bridge); err != nil {
			writeError(w, http.StatusBadGateway, "start guest bridge: "+err.Error())
			return
		}
	}
	inst.Status = models.StatusRunning
	inst.LastRunAt = time.Now().Unix()
	if ip, err := s.docker.IPAddress(ctx, inst.ContainerID); err == nil && ip != "" {
		inst.IPAddress = ip
	}
	if err := s.store.PutInstance(inst); err != nil {
		writeMappedError(w, err)
		return
	}
	s.register(inst)
	writeJSON(w, http.StatusOK, instanceView{Instance: inst})
}

func (s *Server) handleHaltInstance(w http.ResponseWriter, r *http.Request) {
	inst, err := s.store.GetInstance(r.PathValue("id"))
	if err != nil {
		writeMappedError(w, err)
		return
	}
	if inst.ContainerID != "" {
		if err := s.docker.Halt(r.Context(), inst.ContainerID, 10); err != nil && !docker.IsNotFound(err) {
			writeError(w, http.StatusBadGateway, "halt container: "+err.Error())
			return
		}
	}
	inst.Status = models.StatusHalted
	if err := s.store.PutInstance(inst); err != nil {
		writeMappedError(w, err)
		return
	}
	s.unregister(inst.ID)
	writeJSON(w, http.StatusOK, instanceView{Instance: inst})
}

func (s *Server) handleDeleteInstance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	inst, err := s.store.GetInstance(id)
	if err != nil {
		writeMappedError(w, err)
		return
	}
	if inst.ContainerID != "" {
		if err := s.docker.Remove(r.Context(), inst.ContainerID); err != nil && !docker.IsNotFound(err) {
			writeError(w, http.StatusBadGateway, "remove container: "+err.Error())
			return
		}
	}
	if err := s.store.DeleteInstance(id); err != nil {
		writeMappedError(w, err)
		return
	}
	s.unregister(id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleInjectInstance(w http.ResponseWriter, r *http.Request) {
	inst, err := s.store.GetInstance(r.PathValue("id"))
	if err != nil {
		writeMappedError(w, err)
		return
	}
	var req injectRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if inst.ContainerID == "" {
		writeError(w, http.StatusConflict, "instance has no materialised container")
		return
	}
	res := models.Resource{
		HostSourcePath:  req.HostSourcePath,
		GuestTargetPath: req.GuestTargetPath,
		FileMode:        req.FileMode,
	}
	if err := s.docker.InjectResource(r.Context(), inst.ContainerID, res); err != nil {
		writeError(w, http.StatusBadGateway, "inject file: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleResizeExec(w http.ResponseWriter, r *http.Request) {
	instanceID := r.PathValue("id")
	execID := r.PathValue("execID")
	if !s.hasExec(instanceID, execID) {
		writeError(w, http.StatusNotFound, "unknown exec session")
		return
	}
	var req resizeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if err := s.docker.ResizeExec(r.Context(), execID, req.Height, req.Width); err != nil {
		writeError(w, http.StatusBadGateway, "resize: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleConnectInstance opens a fresh interactive shell in the instance's
// container and streams it to the client. Each request creates an independent
// exec session (unlike container attach, which shares PID 1 stdio). The exec ID
// is returned in the X-Vivarium-Exec response header for later resizes.
func (s *Server) handleConnectInstance(w http.ResponseWriter, r *http.Request) {
	inst, err := s.store.GetInstance(r.PathValue("id"))
	if err != nil {
		writeMappedError(w, err)
		return
	}
	if inst.ContainerID == "" {
		writeError(w, http.StatusConflict, "instance has no materialised container")
		return
	}
	// Docker's exec endpoint returns 101 and then hangs for a stopped
	// container, so refuse before upgrading the client connection.
	status, err := s.docker.Status(r.Context(), inst.ContainerID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "container status: "+err.Error())
		return
	}
	if status != models.StatusRunning {
		writeError(w, http.StatusConflict, "container is not running")
		return
	}

	cols, _ := strconv.Atoi(r.URL.Query().Get("cols"))
	rows, _ := strconv.Atoi(r.URL.Query().Get("rows"))

	stream, execID, err := s.docker.Connect(r.Context(), inst.ContainerID, cols, rows)
	if err != nil {
		writeError(w, http.StatusBadGateway, "connect: "+err.Error())
		return
	}
	defer stream.Close()
	s.trackExec(inst.ID, execID)
	defer s.untrackExec(inst.ID, execID)

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		writeError(w, http.StatusInternalServerError, "connection does not support hijacking")
		return
	}
	clientConn, buf, err := hijacker.Hijack()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "hijack: "+err.Error())
		return
	}
	defer clientConn.Close()

	_, _ = buf.WriteString("HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: tcp\r\nConnection: Upgrade\r\n" +
		"X-Vivarium-Exec: " + execID + "\r\n\r\n")
	_ = buf.Flush()

	// Pump both directions and return as soon as either side closes. Waiting for
	// both would hang when the shell exits (the client's stdin copy never ends),
	// leaving the TUI stuck in the session.
	errc := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(stream, clientConn)
		errc <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(clientConn, stream)
		errc <- struct{}{}
	}()
	<-errc
}

// buildEndpoints binds each recipe API key to a fresh dummy token for the
// instance being created.
func buildEndpoints(keys []models.APIKey) []models.InstanceEndpoint {
	out := make([]models.InstanceEndpoint, 0, len(keys))
	for _, k := range keys {
		out = append(out, models.InstanceEndpoint{
			KeyID:        k.ID,
			ProviderType: k.ProviderType,
			BaseURL:      k.BaseURL,
			MockURL:      k.MockURL,
			Token:        "viv-tok-" + randomHex(16),
			RateLimitRPM: k.RateLimitRPM,
		})
	}
	return out
}

// verifyEndpointSecrets fails instance creation early when an endpoint's
// credential cannot be resolved, instead of leaving a broken running instance.
func (s *Server) verifyEndpointSecrets(endpoints []models.InstanceEndpoint) error {
	for _, ep := range endpoints {
		if _, err := s.auth.Secret(ep.KeyID); err != nil {
			return fmt.Errorf("endpoint %s: credential unavailable (%v); re-save the API key", ep.MockURL, err)
		}
	}
	return nil
}

// providerKeyEnv sets the dummy API-key environment variable for standard
// providers. Base URLs are never injected: agents use their built-in defaults
// and the guest relay makes the mock_url reachable. Custom endpoints are
// configured by the user.
func providerKeyEnv(endpoints []models.InstanceEndpoint) map[string]string {
	env := make(map[string]string, len(endpoints))
	for _, ep := range endpoints {
		if ep.ProviderType == models.ProviderCustom {
			continue
		}
		keyVar, _ := models.ProviderEnvNames(ep.ProviderType)
		if keyVar != "" {
			env[keyVar] = ep.Token
		}
	}
	return env
}

// relayForwards maps the guest loopback ports to the unprivileged host bridge.
func relayForwards(tlsPort, httpPort int) []docker.Forward {
	var out []docker.Forward
	if tlsPort > 0 {
		out = append(out, docker.Forward{
			Listen: "127.0.0.1:443",
			Target: net.JoinHostPort(docker.NetworkGateway, strconv.Itoa(tlsPort)),
		})
	}
	if httpPort > 0 {
		out = append(out, docker.Forward{
			Listen: "127.0.0.1:80",
			Target: net.JoinHostPort(docker.NetworkGateway, strconv.Itoa(httpPort)),
		})
	}
	return out
}

// guestBridgeSpec builds the in-guest relay spec, or nil when no bridge is
// configured.
func (s *Server) guestBridgeSpec() (*docker.GuestBridgeSpec, error) {
	if s.proxy.TLSPort <= 0 {
		return nil, nil
	}
	if !guestbridge.Supported() {
		return nil, fmt.Errorf("guest bridge is unavailable on this platform")
	}
	forwards := relayForwards(s.proxy.TLSPort, s.proxy.HTTPPort)
	if len(forwards) == 0 {
		return nil, fmt.Errorf("guest bridge has no forwards")
	}
	return &docker.GuestBridgeSpec{
		Binary:   guestbridge.Binary(),
		Path:     guestbridge.GuestPath,
		Forwards: forwards,
	}, nil
}

// mockHosts returns the sorted unique hostnames of the endpoints' mock URLs.
func mockHosts(endpoints []models.InstanceEndpoint) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, ep := range endpoints {
		u, err := url.Parse(ep.MockURL)
		if err != nil || u.Hostname() == "" {
			continue
		}
		if _, ok := seen[u.Hostname()]; ok {
			continue
		}
		seen[u.Hostname()] = struct{}{}
		out = append(out, u.Hostname())
	}
	sort.Strings(out)
	return out
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

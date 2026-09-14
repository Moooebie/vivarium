// Package api implements the RESTful JSON API served over the Vivarium Unix
// domain socket.
package api

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"

	"vivarium/internal/auth"
	"vivarium/internal/docker"
	"vivarium/internal/models"
	"vivarium/internal/paths"
	"vivarium/internal/store"
)

// InstanceRegistry receives instance lifecycle events so the host bridge can
// register and remove endpoint bindings.
type InstanceRegistry interface {
	Register(models.Instance)
	Unregister(instanceID string)
}

// ProxyBinding carries the bridge details the API needs when creating
// instances: the CA to trust and the dial ports.
type ProxyBinding struct {
	CACertPEM []byte
	TLSPort   int
	HTTPPort  int
}

// Server wires the storage, auth, and Docker subsystems into an HTTP handler.
type Server struct {
	store    *store.Store
	auth     *auth.Manager
	docker   *docker.Controller
	env      paths.Env
	mux      *http.ServeMux
	gpuEnum  docker.GPUEnumerator
	httpServ *http.Server
	registry InstanceRegistry
	proxy    ProxyBinding

	// execs tracks the live interactive exec sessions per instance so that
	// resize requests can be validated against a real session.
	execsMu sync.Mutex
	execs   map[string]map[string]struct{}
}

// NewServer constructs a Server and registers all routes.
func NewServer(st *store.Store, am *auth.Manager, dc *docker.Controller, env paths.Env) *Server {
	s := &Server{
		store:   st,
		auth:    am,
		docker:  dc,
		env:     env,
		mux:     http.NewServeMux(),
		gpuEnum: docker.DefaultGPUEnumerator(),
		execs:   map[string]map[string]struct{}{},
	}
	s.routes()
	return s
}

// trackExec records a live exec session for an instance.
func (s *Server) trackExec(instanceID, execID string) {
	s.execsMu.Lock()
	defer s.execsMu.Unlock()
	set := s.execs[instanceID]
	if set == nil {
		set = map[string]struct{}{}
		s.execs[instanceID] = set
	}
	set[execID] = struct{}{}
}

// untrackExec forgets an exec session.
func (s *Server) untrackExec(instanceID, execID string) {
	s.execsMu.Lock()
	defer s.execsMu.Unlock()
	set := s.execs[instanceID]
	delete(set, execID)
	if len(set) == 0 {
		delete(s.execs, instanceID)
	}
}

// hasExec reports whether execID is a live session for instanceID.
func (s *Server) hasExec(instanceID, execID string) bool {
	s.execsMu.Lock()
	defer s.execsMu.Unlock()
	_, ok := s.execs[instanceID][execID]
	return ok
}

// SetRegistry wires the instance registry used by the host bridge.
func (s *Server) SetRegistry(r InstanceRegistry) { s.registry = r }

// SetProxyBinding supplies the CA and dial ports used for new instances.
func (s *Server) SetProxyBinding(b ProxyBinding) { s.proxy = b }

func (s *Server) register(inst models.Instance) {
	if s.registry != nil {
		s.registry.Register(inst)
	}
}

func (s *Server) unregister(instanceID string) {
	if s.registry != nil {
		s.registry.Unregister(instanceID)
	}
}

// Handler returns the HTTP handler for the API.
func (s *Server) Handler() http.Handler { return s.mux }

// Serve accepts connections on ln until ctx is cancelled.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	s.httpServ = &http.Server{Handler: s.mux}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.httpServ.Shutdown(shutdownCtx)
	}()
	err := s.httpServ.Serve(ln)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/v1/ping", s.handlePing)

	s.mux.HandleFunc("GET /api/v1/auth/status", s.handleAuthStatus)
	s.mux.HandleFunc("POST /api/v1/auth/setup", s.handleAuthSetup)
	s.mux.HandleFunc("POST /api/v1/auth/unlock", s.handleAuthUnlock)

	s.mux.HandleFunc("GET /api/v1/base-images", s.handleListBaseImages)
	s.mux.HandleFunc("POST /api/v1/base-images", s.handleCreateBaseImage)
	s.mux.HandleFunc("POST /api/v1/base-images/rebuild", s.handleRebuildBaseImages)
	s.mux.HandleFunc("DELETE /api/v1/base-images/{id}", s.handleDeleteBaseImage)

	s.mux.HandleFunc("GET /api/v1/api-keys", s.handleListAPIKeys)
	s.mux.HandleFunc("POST /api/v1/api-keys", s.handleCreateAPIKey)
	s.mux.HandleFunc("PUT /api/v1/api-keys/{id}", s.handleUpdateAPIKey)
	s.mux.HandleFunc("DELETE /api/v1/api-keys/{id}", s.handleDeleteAPIKey)
	s.mux.HandleFunc("GET /api/v1/api-keys/{id}/secret", s.handleGetAPIKeySecret)
	s.mux.HandleFunc("POST /api/v1/api-keys/test", s.handleTestAPIKeyPayload)
	s.mux.HandleFunc("POST /api/v1/api-keys/{id}/test", s.handleTestAPIKey)

	s.mux.HandleFunc("GET /api/v1/recipes", s.handleListRecipes)
	s.mux.HandleFunc("POST /api/v1/recipes", s.handleCreateRecipe)
	s.mux.HandleFunc("GET /api/v1/recipes/{id}", s.handleGetRecipe)
	s.mux.HandleFunc("PUT /api/v1/recipes/{id}", s.handleUpdateRecipe)
	s.mux.HandleFunc("DELETE /api/v1/recipes/{id}", s.handleDeleteRecipe)

	s.mux.HandleFunc("GET /api/v1/instances", s.handleListInstances)
	s.mux.HandleFunc("POST /api/v1/instances", s.handleCreateInstance)
	s.mux.HandleFunc("GET /api/v1/instances/{id}", s.handleGetInstance)
	s.mux.HandleFunc("PUT /api/v1/instances/{id}", s.handleUpdateInstance)
	s.mux.HandleFunc("POST /api/v1/instances/{id}/start", s.handleStartInstance)
	s.mux.HandleFunc("POST /api/v1/instances/{id}/halt", s.handleHaltInstance)
	s.mux.HandleFunc("DELETE /api/v1/instances/{id}", s.handleDeleteInstance)
	s.mux.HandleFunc("POST /api/v1/instances/{id}/inject", s.handleInjectInstance)
	s.mux.HandleFunc("GET /api/v1/instances/{id}/connect", s.handleConnectInstance)
	s.mux.HandleFunc("POST /api/v1/instances/{id}/exec/{execID}/resize", s.handleResizeExec)

	s.mux.HandleFunc("GET /api/v1/system/gpus", s.handleListGPUs)
	s.mux.HandleFunc("GET /api/v1/system/status", s.handleSystemStatus)
}

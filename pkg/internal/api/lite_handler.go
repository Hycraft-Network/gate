package api

import (
	"encoding/json"
	"net/http"
	"slices"
	"sync"

	"go.minekube.com/gate/pkg/edition/java/lite/config"
)

// LiteRouter provides HTTP JSON endpoints for managing Lite mode routes at runtime.
// Mount it via LiteRouter.Handler() and register the returned path+handler on the HTTP mux.
//
// Endpoints:
//
//	GET    /gate/lite/routes   – list all routes
//	POST   /gate/lite/routes   – add a route (validated)
//	DELETE /gate/lite/routes   – remove routes matching at least one of the given hosts
type LiteRouter struct {
	mu   sync.RWMutex
	cfg  *config.Config
}

// NewLiteRouter creates a LiteRouter backed by the provided Lite config pointer.
// The caller is responsible for ensuring cfg is not nil.
func NewLiteRouter(cfg *config.Config) *LiteRouter {
	return &LiteRouter{cfg: cfg}
}

// Handler returns the path prefix and the http.Handler to mount on the HTTP mux.
func (lr *LiteRouter) Handler() (string, http.Handler) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /gate/lite/routes", lr.listRoutes)
	mux.HandleFunc("POST /gate/lite/routes", lr.addRoute)
	mux.HandleFunc("DELETE /gate/lite/routes", lr.removeRoutes)
	return "/gate/lite/", mux
}

// ----- list -----

func (lr *LiteRouter) listRoutes(w http.ResponseWriter, r *http.Request) {
	lr.mu.RLock()
	routes := make([]config.Route, len(lr.cfg.Routes))
	copy(routes, lr.cfg.Routes)
	lr.mu.RUnlock()

	writeJSON(w, http.StatusOK, map[string]any{"routes": routes})
}

// ----- add -----

type addRouteRequest struct {
	Route config.Route `json:"route"`
}

func (lr *LiteRouter) addRoute(w http.ResponseWriter, r *http.Request) {
	var req addRouteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	// Validate using the existing config validator
	tmp := config.Config{Enabled: true, Routes: []config.Route{req.Route}}
	_, errs := tmp.Validate()
	if len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"errors": msgs})
		return
	}

	lr.mu.Lock()
	lr.cfg.Routes = append(lr.cfg.Routes, req.Route)
	lr.mu.Unlock()

	writeJSON(w, http.StatusCreated, map[string]any{"message": "route added"})
}

// ----- remove -----

type removeRoutesRequest struct {
	// Hosts is a list of host patterns; any route that contains at least one
	// of these hosts will be removed.
	Hosts []string `json:"hosts"`
}

func (lr *LiteRouter) removeRoutes(w http.ResponseWriter, r *http.Request) {
	var req removeRoutesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if len(req.Hosts) == 0 {
		writeError(w, http.StatusBadRequest, "hosts must not be empty")
		return
	}

	lr.mu.Lock()
	before := len(lr.cfg.Routes)
	lr.cfg.Routes = slices.DeleteFunc(lr.cfg.Routes, func(route config.Route) bool {
		for _, h := range route.Host {
			if slices.Contains(req.Hosts, h) {
				return true
			}
		}
		return false
	})
	removed := before - len(lr.cfg.Routes)
	lr.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{"removed": removed})
}

// ----- helpers -----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

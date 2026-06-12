package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"

	"github.com/gen1nya/wg-admin/agent/internal/geoip"
	"github.com/gen1nya/wg-admin/agent/internal/kernel"
	"github.com/gen1nya/wg-admin/agent/internal/plan"
	"github.com/gen1nya/wg-admin/agent/internal/rttprobe"
	"github.com/gen1nya/wg-admin/agent/internal/store"
)

type Server struct {
	Store  *store.Store
	Kernel kernel.Kernel
	Plan   *plan.Engine

	// Geo resolves peer endpoint IPs to coarse locations for the map view.
	// May be a disabled (no-op) resolver when no geo DB is configured; the
	// nil *geoip.Resolver is also safe to call.
	Geo *geoip.Resolver

	// RTT holds the latest best-effort tunnel-ping measurements per peer. May
	// be nil (probing disabled / mock); the nil *rttprobe.Prober is safe.
	RTT *rttprobe.Prober

	// peerMu serializes peer address allocation + insert so two concurrent
	// POSTs can't read the same "free" address and both claim it. The unique
	// index from migration 0005 is the DB-level backstop; this keeps the
	// race from ever reaching it on the happy path.
	peerMu sync.Mutex
}

// Mux returns the HTTP handler with all routes registered.
func (s *Server) Mux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", s.status)

	mux.HandleFunc("GET /interfaces", s.listInterfaces)
	mux.HandleFunc("GET /interfaces/{name}", s.showInterface)
	mux.HandleFunc("GET /interfaces/{name}/peers", s.listPeersOnInterface)
	mux.HandleFunc("POST /interfaces/{name}/peers", s.createPeer)

	mux.HandleFunc("GET /peers", s.listPeers)
	mux.HandleFunc("GET /peers/{id}", s.getPeer)
	mux.HandleFunc("PATCH /peers/{id}", s.updatePeer)
	mux.HandleFunc("DELETE /peers/{id}", s.deletePeer)
	mux.HandleFunc("GET /peers/{id}/config", s.getPeerConfig)
	mux.HandleFunc("PATCH /peers/{id}/exit", s.updatePeerExit)

	mux.HandleFunc("GET /exits", s.listExits)
	mux.HandleFunc("GET /marks", s.listMarks)
	mux.HandleFunc("GET /traffic", s.listTraffic)
	mux.HandleFunc("GET /geo", s.listGeo)

	mux.HandleFunc("GET /audit", s.listAudit)

	mux.HandleFunc("POST /plan", s.createPlan)
	mux.HandleFunc("GET /plans", s.listPlans)
	mux.HandleFunc("GET /plans/{id}", s.getPlanHandler)
	mux.HandleFunc("POST /plans/{id}/apply", s.applyPlan)
	mux.HandleFunc("POST /plans/{id}/confirm", s.confirmPlan)
	mux.HandleFunc("POST /plans/{id}/revert", s.revertPlan)
	return mux
}

func (s *Server) status(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"kernel_mode": s.Kernel.Version(),
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// statusForErr maps store errors to HTTP status codes.
func statusForErr(err error) int {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

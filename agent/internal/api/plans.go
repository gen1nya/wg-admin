package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/gen1nya/wg-admin/agent/internal/plan"
)

type createPlanReq struct {
	Description string             `json:"description"`
	Desired     plan.DesiredState  `json:"desired"`
}

// createPlan: POST /plan
// Body: {"description": "...", "desired": {"ipsets": [...]}}
// Returns the plan row + computed diff.
func (s *Server) createPlan(w http.ResponseWriter, r *http.Request) {
	if s.Plan == nil {
		writeErr(w, http.StatusServiceUnavailable, "plan engine not initialized")
		return
	}
	var req createPlanReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	p, diff, err := s.Plan.Create(r.Context(), audActor(r), req.Description, req.Desired)
	if err != nil {
		writeErr(w, statusForPlanErr(err), err.Error())
		return
	}
	_ = s.Store.LogAudit(r.Context(), audActor(r), "plan.create", "plan", &p.ID, p.Desired)
	writeJSON(w, http.StatusCreated, map[string]any{
		"plan": p,
		"diff": diff,
	})
}

func (s *Server) listPlans(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	plans, err := s.Store.ListPlans(r.Context(), limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, plans)
}

func (s *Server) getPlanHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	p, err := s.Store.GetPlan(r.Context(), id)
	if err != nil {
		writeErr(w, statusForErr(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// applyPlan: POST /plans/{id}/apply?timeout=30
func (s *Server) applyPlan(w http.ResponseWriter, r *http.Request) {
	if s.Plan == nil {
		writeErr(w, http.StatusServiceUnavailable, "plan engine not initialized")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	timeoutSec := 0
	if v := r.URL.Query().Get("timeout"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			timeoutSec = n
		}
	}
	p, err := s.Plan.Apply(r.Context(), id, timeoutSec)
	if err != nil {
		writeErr(w, statusForPlanErr(err), err.Error())
		return
	}
	_ = s.Store.LogAudit(r.Context(), audActor(r), "plan.apply", "plan", &id, "{}")
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) confirmPlan(w http.ResponseWriter, r *http.Request) {
	if s.Plan == nil {
		writeErr(w, http.StatusServiceUnavailable, "plan engine not initialized")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	p, err := s.Plan.Confirm(r.Context(), id)
	if err != nil {
		writeErr(w, statusForPlanErr(err), err.Error())
		return
	}
	_ = s.Store.LogAudit(r.Context(), audActor(r), "plan.confirm", "plan", &id, "{}")
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) revertPlan(w http.ResponseWriter, r *http.Request) {
	if s.Plan == nil {
		writeErr(w, http.StatusServiceUnavailable, "plan engine not initialized")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	p, err := s.Plan.Revert(r.Context(), id)
	if err != nil {
		writeErr(w, statusForPlanErr(err), err.Error())
		return
	}
	_ = s.Store.LogAudit(r.Context(), audActor(r), "plan.revert", "plan", &id, "{}")
	writeJSON(w, http.StatusOK, p)
}

// statusForPlanErr maps plan-engine errors to HTTP codes.
func statusForPlanErr(err error) int {
	switch {
	case errors.Is(err, plan.ErrInvalidDesired):
		return http.StatusBadRequest
	case errors.Is(err, plan.ErrPlanNotPending),
		errors.Is(err, plan.ErrPlanNotApplied),
		errors.Is(err, plan.ErrAnotherApplied):
		return http.StatusConflict
	}
	return statusForErr(err)
}

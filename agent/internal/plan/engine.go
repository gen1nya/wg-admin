package plan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gen1nya/wg-admin/agent/internal/kernel"
	"github.com/gen1nya/wg-admin/agent/internal/model"
	"github.com/gen1nya/wg-admin/agent/internal/store"
)

const (
	DefaultTimeoutSec = 30
	MinTimeoutSec     = 5
	MaxTimeoutSec     = 300
)

var (
	ErrPlanNotPending  = errors.New("plan is not pending")
	ErrPlanNotApplied  = errors.New("plan is not applied")
	ErrAnotherApplied  = errors.New("another plan is in applied state")
	ErrInvalidDesired  = errors.New("invalid desired state")
)

// Engine coordinates plan creation, apply, confirm, revert.
// One global mutex serializes every state transition: kernel consistency
// matters more than throughput here (apply flows are human-initiated and rare).
type Engine struct {
	Store  *store.Store
	Kernel kernel.Kernel

	Now func() time.Time // clock hook for tests; nil = time.Now

	mu     sync.Mutex
	timers map[int64]*time.Timer
}

func NewEngine(st *store.Store, k kernel.Kernel) *Engine {
	return &Engine{
		Store:  st,
		Kernel: k,
		timers: make(map[int64]*time.Timer),
	}
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// Create persists a pending plan with a freshly-captured snapshot and
// computed diff. Returns the plan row for rendering into the API response.
func (e *Engine) Create(ctx context.Context, createdBy, description string, desired DesiredState) (model.Plan, Diff, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Ownership gates — reject early, before touching the kernel.
	if err := validateRuleOwnership(desired.Rules); err != nil {
		return model.Plan{}, Diff{}, fmt.Errorf("%w: %v", ErrInvalidDesired, err)
	}
	if len(desired.Routes) > 0 {
		owned, err := allowedTables(ctx, e.Store)
		if err != nil {
			return model.Plan{}, Diff{}, fmt.Errorf("load allowed tables: %w", err)
		}
		if err := validateRouteOwnership(desired.Routes, owned); err != nil {
			return model.Plan{}, Diff{}, fmt.Errorf("%w: %v", ErrInvalidDesired, err)
		}
	}
	if desired.NFT != nil {
		if err := validateNFTBody(desired.NFT.Body); err != nil {
			return model.Plan{}, Diff{}, fmt.Errorf("%w: %v", ErrInvalidDesired, err)
		}
	}

	snap, err := e.snapshot(desired)
	if err != nil {
		return model.Plan{}, Diff{}, fmt.Errorf("snapshot: %w", err)
	}
	diff := Diff{
		IPSets: diffIPSets(desired.IPSets, snap.IPSets),
		Routes: diffRoutes(desired.Routes, snap.Routes),
		Rules:  diffRules(desired.Rules, snap.Rules),
		NFT:    diffNFT(desired.NFT, snap.NFT),
	}

	desiredJSON, err := json.Marshal(desired)
	if err != nil {
		return model.Plan{}, Diff{}, err
	}
	diffJSON, err := json.Marshal(diff)
	if err != nil {
		return model.Plan{}, Diff{}, err
	}
	snapJSON, err := json.Marshal(snap)
	if err != nil {
		return model.Plan{}, Diff{}, err
	}

	p := &model.Plan{
		CreatedAt:   e.now().Unix(),
		CreatedBy:   createdBy,
		Description: description,
		Desired:     string(desiredJSON),
		Diff:        string(diffJSON),
		SnapshotPre: string(snapJSON),
		State:       model.PlanPending,
	}
	id, err := e.Store.InsertPlan(ctx, p)
	if err != nil {
		return model.Plan{}, Diff{}, err
	}
	p.ID = id
	return *p, diff, nil
}

// Apply runs the plan against the kernel and schedules a watchdog revert.
// Only one plan may be in 'applied' state at a time.
func (e *Engine) Apply(ctx context.Context, id int64, timeoutSec int) (model.Plan, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	timeoutSec = clampTimeout(timeoutSec)

	// Only one applied plan at a time.
	applied, err := e.Store.ListPlansByState(ctx, model.PlanApplied)
	if err != nil {
		return model.Plan{}, err
	}
	if len(applied) > 0 {
		return model.Plan{}, fmt.Errorf("%w: plan %d", ErrAnotherApplied, applied[0].ID)
	}

	plan, err := e.Store.GetPlan(ctx, id)
	if err != nil {
		return model.Plan{}, err
	}
	if plan.State != model.PlanPending {
		return model.Plan{}, fmt.Errorf("%w: state=%s", ErrPlanNotPending, plan.State)
	}

	var desired DesiredState
	if err := json.Unmarshal([]byte(plan.Desired), &desired); err != nil {
		return model.Plan{}, fmt.Errorf("decode desired: %w", err)
	}
	var snap Snapshot
	if err := json.Unmarshal([]byte(plan.SnapshotPre), &snap); err != nil {
		return model.Plan{}, fmt.Errorf("decode snapshot: %w", err)
	}

	// Apply order respects dependencies: ipsets → nft → rules → routes.
	// nft may reference ipsets by name; rules use marks set by nft;
	// routes live in named tables selected by rules.
	if err := applyIPSets(e.Kernel, desired.IPSets, snap.IPSets); err != nil {
		return model.Plan{}, err
	}
	if err := applyNFT(e.Kernel, desired.NFT); err != nil {
		_ = revertAllIPSets(e.Kernel, snap.IPSets)
		return model.Plan{}, err
	}
	if err := applyRules(e.Kernel, desired.Rules, snap.Rules); err != nil {
		_ = revertNFT(e.Kernel, snap.NFT)
		_ = revertAllIPSets(e.Kernel, snap.IPSets)
		return model.Plan{}, err
	}
	if err := applyRoutes(e.Kernel, desired.Routes, snap.Routes); err != nil {
		_ = revertAllRules(e.Kernel, snap.Rules)
		_ = revertNFT(e.Kernel, snap.NFT)
		_ = revertAllIPSets(e.Kernel, snap.IPSets)
		return model.Plan{}, err
	}

	appliedAt := e.now().Unix()
	if err := e.Store.MarkApplied(ctx, id, plan.SnapshotPre, timeoutSec, appliedAt); err != nil {
		// Kernel is applied but DB failed — unusual. Revert every resource.
		_ = revertAllRoutes(e.Kernel, snap.Routes)
		_ = revertAllRules(e.Kernel, snap.Rules)
		_ = revertNFT(e.Kernel, snap.NFT)
		_ = revertAllIPSets(e.Kernel, snap.IPSets)
		return model.Plan{}, fmt.Errorf("mark applied: %w", err)
	}

	e.scheduleWatchdog(id, time.Duration(timeoutSec)*time.Second)

	plan, _ = e.Store.GetPlan(ctx, id)
	return plan, nil
}

// Confirm cancels the watchdog and marks the plan confirmed.
func (e *Engine) Confirm(ctx context.Context, id int64) (model.Plan, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	plan, err := e.Store.GetPlan(ctx, id)
	if err != nil {
		return model.Plan{}, err
	}
	if plan.State != model.PlanApplied {
		return model.Plan{}, fmt.Errorf("%w: state=%s", ErrPlanNotApplied, plan.State)
	}

	e.cancelTimer(id)

	if err := e.Store.MarkConfirmed(ctx, id, e.now().Unix()); err != nil {
		return model.Plan{}, err
	}
	plan, _ = e.Store.GetPlan(ctx, id)
	return plan, nil
}

// Revert manually rolls back an applied plan. Uses the stored snapshot.
func (e *Engine) Revert(ctx context.Context, id int64) (model.Plan, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.revertLocked(ctx, id, model.PlanReverted)
}

// revertLocked: caller holds e.mu. finalState is PlanReverted or PlanExpired.
func (e *Engine) revertLocked(ctx context.Context, id int64, finalState string) (model.Plan, error) {
	plan, err := e.Store.GetPlan(ctx, id)
	if err != nil {
		return model.Plan{}, err
	}
	if plan.State != model.PlanApplied {
		return model.Plan{}, fmt.Errorf("%w: state=%s", ErrPlanNotApplied, plan.State)
	}

	e.cancelTimer(id)

	var snap Snapshot
	if err := json.Unmarshal([]byte(plan.SnapshotPre), &snap); err != nil {
		return model.Plan{}, fmt.Errorf("decode snapshot: %w", err)
	}
	// Revert order is reverse of apply: routes → rules → nft → ipsets.
	// Each revert is best-effort; we keep going and surface the first error.
	var revertErr error
	if err := revertAllRoutes(e.Kernel, snap.Routes); err != nil && revertErr == nil {
		revertErr = err
	}
	if err := revertAllRules(e.Kernel, snap.Rules); err != nil && revertErr == nil {
		revertErr = err
	}
	if err := revertNFT(e.Kernel, snap.NFT); err != nil && revertErr == nil {
		revertErr = err
	}
	if err := revertAllIPSets(e.Kernel, snap.IPSets); err != nil && revertErr == nil {
		revertErr = err
	}
	if revertErr != nil {
		return model.Plan{}, fmt.Errorf("revert: %w", revertErr)
	}
	if err := e.Store.MarkReverted(ctx, id, finalState, e.now().Unix()); err != nil {
		return model.Plan{}, err
	}
	plan, _ = e.Store.GetPlan(ctx, id)
	return plan, nil
}

// Recover runs at agent startup. Any plan still in 'applied' state is
// either past its deadline (auto-revert as 'expired') or re-scheduled
// for its remaining time. Crashed mid-apply plans stay 'pending' — safe.
func (e *Engine) Recover(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	applied, err := e.Store.ListPlansByState(ctx, model.PlanApplied)
	if err != nil {
		return err
	}
	now := e.now().Unix()
	for _, p := range applied {
		if p.AppliedAt == nil {
			slog.Warn("applied plan without applied_at; reverting", "id", p.ID)
			if _, err := e.revertLocked(ctx, p.ID, model.PlanExpired); err != nil {
				slog.Error("recover revert", "id", p.ID, "err", err)
			}
			continue
		}
		deadline := *p.AppliedAt + int64(p.TimeoutSec)
		if now >= deadline {
			slog.Info("recover: plan past deadline, reverting", "id", p.ID, "overdue_sec", now-deadline)
			if _, err := e.revertLocked(ctx, p.ID, model.PlanExpired); err != nil {
				slog.Error("recover revert", "id", p.ID, "err", err)
			}
			continue
		}
		remaining := time.Duration(deadline-now) * time.Second
		slog.Info("recover: rescheduling watchdog", "id", p.ID, "remaining", remaining)
		e.scheduleWatchdog(p.ID, remaining)
	}
	return nil
}

// ReconcileBoot re-applies the last confirmed plan's desired state to the
// live kernel. It's called once at agent startup to heal state that doesn't
// survive reboot (nft tables, ip rules, routes in custom tables, ipsets).
//
// Unlike Apply, this does NOT create a new plan row, capture a rollback
// snapshot, or schedule a watchdog — reboot reconcile is recovery, not a
// policy change. Errors in individual steps are logged and skipped; the
// agent continues so the operator can intervene via API/CLI.
//
// Returns nil if no confirmed plan exists (fresh install).
func (e *Engine) ReconcileBoot(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	last, err := e.Store.LastConfirmedPlan(ctx)
	if err != nil {
		return fmt.Errorf("load last confirmed plan: %w", err)
	}
	if last == nil {
		slog.Info("reconcile: no confirmed plan in DB, skipping")
		return nil
	}

	var desired DesiredState
	if err := json.Unmarshal([]byte(last.Desired), &desired); err != nil {
		return fmt.Errorf("decode desired of plan %d: %w", last.ID, err)
	}

	// Re-run ownership validation. If the plan was authored before ownership
	// rules tightened, we'd rather refuse to auto-apply than widen scope.
	if err := validateRuleOwnership(desired.Rules); err != nil {
		return fmt.Errorf("plan %d: rule ownership: %w", last.ID, err)
	}
	if len(desired.Routes) > 0 {
		owned, err := allowedTables(ctx, e.Store)
		if err != nil {
			return fmt.Errorf("load allowed tables: %w", err)
		}
		if err := validateRouteOwnership(desired.Routes, owned); err != nil {
			return fmt.Errorf("plan %d: route ownership: %w", last.ID, err)
		}
	}
	if desired.NFT != nil {
		if err := validateNFTBody(desired.NFT.Body); err != nil {
			return fmt.Errorf("plan %d: nft body: %w", last.ID, err)
		}
	}

	// Snapshot the LIVE kernel now — last.SnapshotPre was the pre-apply state
	// from hours/days ago and is useless for a post-reboot diff.
	snap, err := e.snapshot(desired)
	if err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}

	// Best-effort apply. Unlike Engine.Apply we don't rollback on partial
	// failure — boot reconcile is idempotent recovery, not an atomic change.
	// Keep the same dependency order as Apply: ipsets → nft → rules → routes.
	var firstErr error
	record := func(stage string, err error) {
		if err == nil {
			return
		}
		slog.Error("reconcile step failed", "stage", stage, "plan_id", last.ID, "err", err)
		if firstErr == nil {
			firstErr = fmt.Errorf("%s: %w", stage, err)
		}
	}
	record("ipsets", applyIPSets(e.Kernel, desired.IPSets, snap.IPSets))
	record("nft", applyNFT(e.Kernel, desired.NFT))
	record("rules", applyRules(e.Kernel, desired.Rules, snap.Rules))
	record("routes", applyRoutes(e.Kernel, desired.Routes, snap.Routes))

	// Audit regardless — operator should see what happened even on partial fail.
	payload := fmt.Sprintf(`{"plan_id":%d,"error":%q}`, last.ID, errStr(firstErr))
	_ = e.Store.LogAudit(ctx, "boot-reconcile", "boot.reconcile.desired", "plan", &last.ID, payload)

	if firstErr != nil {
		slog.Warn("reconcile completed with errors", "plan_id", last.ID, "first_err", firstErr)
		return firstErr
	}
	slog.Info("reconcile: desired state applied", "plan_id", last.ID)
	return nil
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// --- internals ---

func (e *Engine) snapshot(desired DesiredState) (Snapshot, error) {
	var snap Snapshot
	ipsets, err := snapshotIPSets(e.Kernel, desired.IPSets)
	if err != nil {
		return snap, err
	}
	snap.IPSets = ipsets
	routes, err := snapshotRoutes(e.Kernel, desired.Routes)
	if err != nil {
		return snap, err
	}
	snap.Routes = routes
	rules, err := snapshotRules(e.Kernel, desired.Rules)
	if err != nil {
		return snap, err
	}
	snap.Rules = rules
	nft, err := snapshotNFT(e.Kernel, desired.NFT)
	if err != nil {
		return snap, err
	}
	snap.NFT = nft
	return snap, nil
}

// scheduleWatchdog starts (or replaces) an AfterFunc that calls watchdogFired.
// Must be called with e.mu held.
func (e *Engine) scheduleWatchdog(id int64, after time.Duration) {
	if t, ok := e.timers[id]; ok {
		t.Stop()
	}
	e.timers[id] = time.AfterFunc(after, func() {
		e.watchdogFired(id)
	})
}

func (e *Engine) cancelTimer(id int64) {
	if t, ok := e.timers[id]; ok {
		t.Stop()
		delete(e.timers, id)
	}
}

// watchdogFired runs on the timer goroutine. It may race against
// Confirm/Revert which cancel the timer under e.mu — in that case DB state
// will already be non-applied and we exit cleanly.
func (e *Engine) watchdogFired(id int64) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Timer may have been replaced; trust the DB.
	ctx := context.Background()
	plan, err := e.Store.GetPlan(ctx, id)
	if err != nil {
		slog.Error("watchdog: get plan", "id", id, "err", err)
		return
	}
	if plan.State != model.PlanApplied {
		return // already confirmed or reverted elsewhere
	}
	slog.Warn("watchdog fired: reverting plan", "id", id)
	if _, err := e.revertLocked(ctx, id, model.PlanExpired); err != nil {
		slog.Error("watchdog revert", "id", id, "err", err)
	}
}

func clampTimeout(sec int) int {
	if sec <= 0 {
		return DefaultTimeoutSec
	}
	if sec < MinTimeoutSec {
		return MinTimeoutSec
	}
	if sec > MaxTimeoutSec {
		return MaxTimeoutSec
	}
	return sec
}

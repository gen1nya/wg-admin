package plan_test

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/gen1nya/wg-admin/agent/internal/kernel"
	"github.com/gen1nya/wg-admin/agent/internal/model"
	"github.com/gen1nya/wg-admin/agent/internal/plan"
	"github.com/gen1nya/wg-admin/agent/internal/store"
)

// newEngine: fresh store + mock kernel, no pre-seeded ipsets unless test adds any.
func newEngine(t *testing.T) (*plan.Engine, *kernel.Mock) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plans.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	k := kernel.NewMock()
	// Wipe seed state so tests start empty.
	for name := range k.IPSets {
		delete(k.IPSets, name)
	}
	return plan.NewEngine(st, k), k
}

func liveSet(t *testing.T, k *kernel.Mock, name string) []string {
	t.Helper()
	entries, _ := k.IPSetList(name)
	sort.Strings(entries)
	return entries
}

func TestCreateProducesDiff(t *testing.T) {
	e, k := newEngine(t)
	// preseed one existing set
	_ = k.IPSetReplace("direct", []string{"77.88.0.0/16"})

	ctx := context.Background()
	desired := plan.DesiredState{IPSets: []plan.IPSetSpec{
		{Name: "direct", Entries: []string{"77.88.0.0/16", "87.250.0.0/16"}},
		{Name: "telegram-dc", Entries: []string{"91.108.0.0/16"}},
	}}
	p, diff, err := e.Create(ctx, "cli", "test", desired)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.State != model.PlanPending {
		t.Errorf("state=%q", p.State)
	}
	if len(diff.IPSets) != 2 {
		t.Fatalf("diff ipsets=%d, want 2", len(diff.IPSets))
	}

	byName := map[string]plan.IPSetDiff{}
	for _, d := range diff.IPSets {
		byName[d.Name] = d
	}
	direct := byName["direct"]
	if direct.Created {
		t.Error("direct should not be 'created' (already exists)")
	}
	if len(direct.Add) != 1 || direct.Add[0] != "87.250.0.0/16" {
		t.Errorf("direct.Add=%v, want [87.250.0.0/16]", direct.Add)
	}
	tg := byName["telegram-dc"]
	if !tg.Created {
		t.Error("telegram-dc should be 'created' (fresh)")
	}
	if len(tg.Add) != 1 || tg.Add[0] != "91.108.0.0/16" {
		t.Errorf("telegram-dc.Add=%v", tg.Add)
	}

	// Snapshot must have been captured and stored.
	if p.SnapshotPre == "" {
		t.Error("snapshot not persisted")
	}
}

func TestApplyConfirmFlow(t *testing.T) {
	e, k := newEngine(t)
	ctx := context.Background()

	desired := plan.DesiredState{IPSets: []plan.IPSetSpec{
		{Name: "direct", Entries: []string{"77.88.0.0/16"}},
	}}
	p, _, _ := e.Create(ctx, "cli", "", desired)

	applied, err := e.Apply(ctx, p.ID, 60)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if applied.State != model.PlanApplied {
		t.Errorf("state after apply=%q", applied.State)
	}
	if applied.TimeoutSec != 60 {
		t.Errorf("timeout=%d", applied.TimeoutSec)
	}

	// Kernel should have the desired entries.
	if got := liveSet(t, k, "direct"); len(got) != 1 || got[0] != "77.88.0.0/16" {
		t.Errorf("kernel=%v", got)
	}

	confirmed, err := e.Confirm(ctx, p.ID)
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if confirmed.State != model.PlanConfirmed {
		t.Errorf("state after confirm=%q", confirmed.State)
	}
}

func TestManualRevertRestoresKernel(t *testing.T) {
	e, k := newEngine(t)
	_ = k.IPSetReplace("direct", []string{"old-1.0.0.0/24"})

	ctx := context.Background()
	desired := plan.DesiredState{IPSets: []plan.IPSetSpec{
		{Name: "direct", Entries: []string{"new-2.0.0.0/24"}},
	}}
	p, _, _ := e.Create(ctx, "cli", "", desired)
	_, _ = e.Apply(ctx, p.ID, 60)

	// Live kernel now has the new entry.
	if got := liveSet(t, k, "direct"); len(got) != 1 || got[0] != "new-2.0.0.0/24" {
		t.Fatalf("after apply kernel=%v", got)
	}

	reverted, err := e.Revert(ctx, p.ID)
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	if reverted.State != model.PlanReverted {
		t.Errorf("state after revert=%q", reverted.State)
	}
	// Kernel should be back to the old state.
	if got := liveSet(t, k, "direct"); len(got) != 1 || got[0] != "old-1.0.0.0/24" {
		t.Errorf("after revert kernel=%v, want [old-1.0.0.0/24]", got)
	}
}

func TestRevertDestroysCreatedSets(t *testing.T) {
	e, k := newEngine(t)
	ctx := context.Background()

	desired := plan.DesiredState{IPSets: []plan.IPSetSpec{
		{Name: "fresh", Entries: []string{"1.2.3.0/24"}},
	}}
	p, _, _ := e.Create(ctx, "cli", "", desired)
	_, _ = e.Apply(ctx, p.ID, 60)

	if _, err := k.IPSetList("fresh"); err != nil {
		t.Fatalf("expected fresh to exist after apply")
	}

	if _, err := e.Revert(ctx, p.ID); err != nil {
		t.Fatalf("revert: %v", err)
	}
	if _, err := k.IPSetList("fresh"); !errors.Is(err, kernel.ErrIPSetNotFound) {
		t.Errorf("fresh should be destroyed after revert, got err=%v", err)
	}
}

func TestOnlyOneAppliedAtATime(t *testing.T) {
	e, _ := newEngine(t)
	ctx := context.Background()

	d := plan.DesiredState{IPSets: []plan.IPSetSpec{{Name: "a", Entries: []string{"1.0.0.0/24"}}}}
	p1, _, _ := e.Create(ctx, "cli", "", d)
	p2, _, _ := e.Create(ctx, "cli", "", d)

	if _, err := e.Apply(ctx, p1.ID, 60); err != nil {
		t.Fatalf("apply p1: %v", err)
	}
	_, err := e.Apply(ctx, p2.ID, 60)
	if !errors.Is(err, plan.ErrAnotherApplied) {
		t.Errorf("second apply: %v, want ErrAnotherApplied", err)
	}
}

// TestRecoverRevertsExpiredPlan: a plan applied "in the past" whose deadline
// has passed must be reverted by Recover. This exercises the same code path
// the watchdog fires, without having to wait real seconds.
func TestRecoverRevertsExpiredPlan(t *testing.T) {
	e, k := newEngine(t)
	_ = k.IPSetReplace("direct", []string{"old.0.0.0/24"})

	ctx := context.Background()
	desired := plan.DesiredState{IPSets: []plan.IPSetSpec{
		{Name: "direct", Entries: []string{"new.0.0.0/24"}},
	}}

	// Pin clock to T0 so applied_at lands at a known past instant.
	t0 := time.Unix(1_700_000_000, 0)
	e.Now = func() time.Time { return t0 }

	p, _, _ := e.Create(ctx, "cli", "", desired)
	if _, err := e.Apply(ctx, p.ID, 30); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Advance clock past deadline (applied_at + timeout_sec = t0 + 30s).
	e.Now = func() time.Time { return t0.Add(60 * time.Second) }
	if err := e.Recover(ctx); err != nil {
		t.Fatalf("recover: %v", err)
	}

	if got := liveSet(t, k, "direct"); len(got) != 1 || got[0] != "old.0.0.0/24" {
		t.Errorf("after recover kernel=%v, want old", got)
	}
	final, _ := e.Store.GetPlan(ctx, p.ID)
	if final.State != model.PlanExpired {
		t.Errorf("state=%q, want expired", final.State)
	}
}

// TestWatchdogAutoReverts: real timer path, small real timeout. Safeguards
// that the AfterFunc actually fires. Timeout=5s is the clamped minimum.
func TestWatchdogAutoReverts(t *testing.T) {
	if testing.Short() {
		t.Skip("-short: skip 5s watchdog wait")
	}
	e, k := newEngine(t)
	_ = k.IPSetReplace("direct", []string{"old.0.0.0/24"})

	ctx := context.Background()
	desired := plan.DesiredState{IPSets: []plan.IPSetSpec{
		{Name: "direct", Entries: []string{"new.0.0.0/24"}},
	}}
	p, _, _ := e.Create(ctx, "cli", "", desired)

	if _, err := e.Apply(ctx, p.ID, 1); err != nil {
		// 1s gets clamped to MinTimeoutSec=5s.
		t.Fatalf("apply: %v", err)
	}

	// Wait slightly past clamped timeout.
	deadline := time.Now().Add(7 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		p, _ := e.Store.GetPlan(ctx, p.ID)
		if p.State == model.PlanExpired {
			break
		}
	}

	if got := liveSet(t, k, "direct"); len(got) != 1 || got[0] != "old.0.0.0/24" {
		t.Errorf("kernel=%v, want old", got)
	}
	final, _ := e.Store.GetPlan(ctx, p.ID)
	if final.State != model.PlanExpired {
		t.Errorf("state=%q, want expired", final.State)
	}
}

func TestReconcileBootNoPlan(t *testing.T) {
	e, _ := newEngine(t)
	ctx := context.Background()
	// Empty DB — should be a clean no-op, no error.
	if err := e.ReconcileBoot(ctx); err != nil {
		t.Errorf("reconcile on empty db: %v", err)
	}
}

func TestReconcileBootReapplies(t *testing.T) {
	e, k := newEngine(t)
	ctx := context.Background()

	desired := plan.DesiredState{IPSets: []plan.IPSetSpec{
		{Name: "direct", Entries: []string{"77.88.0.0/16", "87.250.0.0/16"}},
	}}
	p, _, _ := e.Create(ctx, "cli", "initial", desired)
	if _, err := e.Apply(ctx, p.ID, 60); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := e.Confirm(ctx, p.ID); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	// Simulate reboot: wipe the ipset from the kernel, leave DB as-is.
	_ = k.IPSetDestroy("direct")
	if got := liveSet(t, k, "direct"); len(got) != 0 {
		t.Fatalf("precondition: ipset should be wiped, got %v", got)
	}

	if err := e.ReconcileBoot(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	got := liveSet(t, k, "direct")
	if len(got) != 2 || got[0] != "77.88.0.0/16" || got[1] != "87.250.0.0/16" {
		t.Errorf("after reconcile: got %v, want [77.88.0.0/16 87.250.0.0/16]", got)
	}
}

func TestReconcileBootIdempotent(t *testing.T) {
	e, k := newEngine(t)
	ctx := context.Background()

	desired := plan.DesiredState{IPSets: []plan.IPSetSpec{
		{Name: "direct", Entries: []string{"77.88.0.0/16"}},
	}}
	p, _, _ := e.Create(ctx, "cli", "", desired)
	_, _ = e.Apply(ctx, p.ID, 60)
	_, _ = e.Confirm(ctx, p.ID)

	// Kernel already matches desired — reconcile must be a no-op, not error.
	if err := e.ReconcileBoot(ctx); err != nil {
		t.Errorf("reconcile on matching kernel: %v", err)
	}
	if got := liveSet(t, k, "direct"); len(got) != 1 || got[0] != "77.88.0.0/16" {
		t.Errorf("after idempotent reconcile: %v", got)
	}
}

func TestReconcileBootPicksLatestConfirmed(t *testing.T) {
	e, k := newEngine(t)
	ctx := context.Background()

	// First plan: one entry.
	d1 := plan.DesiredState{IPSets: []plan.IPSetSpec{
		{Name: "direct", Entries: []string{"1.1.1.0/24"}},
	}}
	p1, _, _ := e.Create(ctx, "cli", "v1", d1)
	_, _ = e.Apply(ctx, p1.ID, 60)
	_, _ = e.Confirm(ctx, p1.ID)

	// Second plan supersedes — two entries.
	d2 := plan.DesiredState{IPSets: []plan.IPSetSpec{
		{Name: "direct", Entries: []string{"2.2.2.0/24", "3.3.3.0/24"}},
	}}
	p2, _, _ := e.Create(ctx, "cli", "v2", d2)
	_, _ = e.Apply(ctx, p2.ID, 60)
	_, _ = e.Confirm(ctx, p2.ID)

	// Wipe kernel, reconcile. Expect v2's entries, not v1's.
	_ = k.IPSetDestroy("direct")
	if err := e.ReconcileBoot(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	got := liveSet(t, k, "direct")
	if len(got) != 2 || got[0] != "2.2.2.0/24" || got[1] != "3.3.3.0/24" {
		t.Errorf("want v2 entries, got %v", got)
	}
}

func TestConfirmAfterRevertRejected(t *testing.T) {
	e, _ := newEngine(t)
	ctx := context.Background()
	d := plan.DesiredState{IPSets: []plan.IPSetSpec{{Name: "a", Entries: []string{"1.0.0.0/24"}}}}
	p, _, _ := e.Create(ctx, "cli", "", d)
	_, _ = e.Apply(ctx, p.ID, 60)
	_, _ = e.Revert(ctx, p.ID)

	_, err := e.Confirm(ctx, p.ID)
	if !errors.Is(err, plan.ErrPlanNotApplied) {
		t.Errorf("confirm after revert: %v, want ErrPlanNotApplied", err)
	}
}

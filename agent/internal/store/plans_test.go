package store_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gen1nya/wg-admin/agent/internal/model"
	"github.com/gen1nya/wg-admin/agent/internal/store"
)

func openPlansStore(t *testing.T) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plans.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestPlanInsertAndGet(t *testing.T) {
	st := openPlansStore(t)
	ctx := context.Background()
	id, err := st.InsertPlan(ctx, &model.Plan{
		CreatedBy: "cli", Description: "test",
		Desired: `{"ipsets":[]}`, Diff: `{"ipsets":[]}`,
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	p, err := st.GetPlan(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if p.State != model.PlanPending {
		t.Errorf("state=%q, want pending", p.State)
	}
	if p.Desired != `{"ipsets":[]}` {
		t.Errorf("desired=%q", p.Desired)
	}
	if p.CreatedAt == 0 {
		t.Error("created_at not set")
	}
}

func TestPlanStateMachine(t *testing.T) {
	st := openPlansStore(t)
	ctx := context.Background()
	id, _ := st.InsertPlan(ctx, &model.Plan{Desired: "{}", Diff: "{}"})

	// pending → applied
	if err := st.MarkApplied(ctx, id, `{"ipsets":[]}`, 30, 1700000000); err != nil {
		t.Fatalf("mark applied: %v", err)
	}
	p, _ := st.GetPlan(ctx, id)
	if p.State != model.PlanApplied || p.AppliedAt == nil || p.TimeoutSec != 30 {
		t.Errorf("applied state: %+v", p)
	}
	if p.SnapshotPre != `{"ipsets":[]}` {
		t.Errorf("snapshot: %q", p.SnapshotPre)
	}

	// applied → confirmed
	if err := st.MarkConfirmed(ctx, id, 1700000010); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	p, _ = st.GetPlan(ctx, id)
	if p.State != model.PlanConfirmed || p.ConfirmedAt == nil {
		t.Errorf("confirmed state: %+v", p)
	}

	// Confirm again → rejected (not in applied anymore)
	if err := st.MarkConfirmed(ctx, id, 1700000020); err == nil {
		t.Error("double confirm should fail")
	}
}

func TestPlanRejectsDoubleApply(t *testing.T) {
	st := openPlansStore(t)
	ctx := context.Background()
	id, _ := st.InsertPlan(ctx, &model.Plan{Desired: "{}", Diff: "{}"})

	if err := st.MarkApplied(ctx, id, "{}", 30, 1); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	err := st.MarkApplied(ctx, id, "{}", 30, 2)
	if err == nil {
		t.Fatal("second apply should fail")
	}
	if !strings.Contains(err.Error(), "pending") {
		t.Errorf("error should mention pending state: %v", err)
	}
}

func TestPlanRevertPath(t *testing.T) {
	st := openPlansStore(t)
	ctx := context.Background()
	id, _ := st.InsertPlan(ctx, &model.Plan{Desired: "{}", Diff: "{}"})
	_ = st.MarkApplied(ctx, id, "{}", 30, 1)

	if err := st.MarkReverted(ctx, id, model.PlanReverted, 2); err != nil {
		t.Fatalf("revert: %v", err)
	}
	p, _ := st.GetPlan(ctx, id)
	if p.State != model.PlanReverted || p.RevertedAt == nil {
		t.Errorf("reverted state: %+v", p)
	}
}

func TestPlanExpiredPath(t *testing.T) {
	st := openPlansStore(t)
	ctx := context.Background()
	id, _ := st.InsertPlan(ctx, &model.Plan{Desired: "{}", Diff: "{}"})
	_ = st.MarkApplied(ctx, id, "{}", 30, 1)

	if err := st.MarkReverted(ctx, id, model.PlanExpired, 100); err != nil {
		t.Fatalf("expire: %v", err)
	}
	p, _ := st.GetPlan(ctx, id)
	if p.State != model.PlanExpired {
		t.Errorf("state=%q, want expired", p.State)
	}
}

func TestPlanListByState(t *testing.T) {
	st := openPlansStore(t)
	ctx := context.Background()
	id1, _ := st.InsertPlan(ctx, &model.Plan{Desired: "{}", Diff: "{}"})
	id2, _ := st.InsertPlan(ctx, &model.Plan{Desired: "{}", Diff: "{}"})
	_ = st.MarkApplied(ctx, id1, "{}", 30, 1)
	_ = st.MarkApplied(ctx, id2, "{}", 30, 2)
	_ = st.MarkConfirmed(ctx, id2, 3)

	applied, err := st.ListPlansByState(ctx, model.PlanApplied)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(applied) != 1 || applied[0].ID != id1 {
		t.Errorf("applied list: %+v", applied)
	}
}

func TestLastConfirmedPlan(t *testing.T) {
	st := openPlansStore(t)
	ctx := context.Background()

	got, err := st.LastConfirmedPlan(ctx)
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	if got != nil {
		t.Errorf("empty db: want nil, got %+v", got)
	}

	// Three plans; confirm #1 and #3, revert #2. Expect #3 back.
	id1, _ := st.InsertPlan(ctx, &model.Plan{Desired: `{"n":1}`, Diff: "{}"})
	id2, _ := st.InsertPlan(ctx, &model.Plan{Desired: `{"n":2}`, Diff: "{}"})
	id3, _ := st.InsertPlan(ctx, &model.Plan{Desired: `{"n":3}`, Diff: "{}"})

	_ = st.MarkApplied(ctx, id1, "{}", 30, 100)
	_ = st.MarkConfirmed(ctx, id1, 110)

	_ = st.MarkApplied(ctx, id2, "{}", 30, 200)
	_ = st.MarkReverted(ctx, id2, model.PlanReverted, 210)

	_ = st.MarkApplied(ctx, id3, "{}", 30, 300)
	_ = st.MarkConfirmed(ctx, id3, 310)

	got, err = st.LastConfirmedPlan(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if got == nil {
		t.Fatal("want plan, got nil")
	}
	if got.ID != id3 {
		t.Errorf("id=%d, want %d (plan #3 had latest confirmed_at)", got.ID, id3)
	}
	if got.Desired != `{"n":3}` {
		t.Errorf("desired=%q", got.Desired)
	}
}

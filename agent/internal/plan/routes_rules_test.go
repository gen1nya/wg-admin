package plan_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gen1nya/wg-admin/agent/internal/kernel"
	"github.com/gen1nya/wg-admin/agent/internal/model"
	"github.com/gen1nya/wg-admin/agent/internal/plan"
)

// seedAllowedTable inserts a mark that makes the given table allow-listed
// for plan ownership.
func seedAllowedTable(t *testing.T, e *plan.Engine, name string, fwmark int) {
	t.Helper()
	ctx := context.Background()
	// marks has UNIQUE(fwmark) and UNIQUE(name) — use unique values per call.
	markID, err := e.Store.UpsertMark(ctx, &model.Mark{Fwmark: fwmark, Name: name, RoutingTable: name})
	if err != nil {
		t.Fatalf("seed mark: %v", err)
	}
	_ = markID
}

func TestRouteApplyConfirmRevert(t *testing.T) {
	e, k := newEngine(t)
	seedAllowedTable(t, e, "wg_tunnel", 0x1)
	ctx := context.Background()

	desired := plan.DesiredState{
		Routes: []kernel.RouteEntry{
			{Table: "wg_tunnel", Dest: "default", Dev: "lo"},
			{Table: "wg_tunnel", Dest: "10.77.0.0/24", Dev: "lo"},
		},
	}
	p, diff, err := e.Create(ctx, "cli", "", desired)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(diff.Routes) != 2 {
		t.Fatalf("diff routes=%d", len(diff.Routes))
	}
	for _, d := range diff.Routes {
		if d.Op != "create" {
			t.Errorf("expected create, got %q for %+v", d.Op, d)
		}
	}

	if _, err := e.Apply(ctx, p.ID, 30); err != nil {
		t.Fatalf("apply: %v", err)
	}
	live, _ := k.RouteList("wg_tunnel")
	if len(live) != 2 {
		t.Errorf("kernel routes=%d", len(live))
	}

	if _, err := e.Revert(ctx, p.ID); err != nil {
		t.Fatalf("revert: %v", err)
	}
	live, _ = k.RouteList("wg_tunnel")
	if len(live) != 0 {
		t.Errorf("after revert routes=%v", live)
	}
}

func TestRuleApplyConfirmRevert(t *testing.T) {
	e, k := newEngine(t)
	seedAllowedTable(t, e, "wg_tunnel", 0x1)
	ctx := context.Background()

	desired := plan.DesiredState{
		Rules: []kernel.RuleEntry{
			{Priority: 10500, Fwmark: 0x1, Table: "wg_tunnel"},
		},
	}
	p, _, err := e.Create(ctx, "cli", "", desired)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := e.Apply(ctx, p.ID, 30); err != nil {
		t.Fatalf("apply: %v", err)
	}
	rules, _ := k.RuleList()
	found := false
	for _, r := range rules {
		if r.Priority == 10500 && r.Fwmark == 1 && r.Table == "wg_tunnel" {
			found = true
		}
	}
	if !found {
		t.Errorf("rule not added: %+v", rules)
	}

	if _, err := e.Revert(ctx, p.ID); err != nil {
		t.Fatalf("revert: %v", err)
	}
	rules, _ = k.RuleList()
	for _, r := range rules {
		if r.Priority == 10500 {
			t.Errorf("rule still present after revert: %+v", r)
		}
	}
}

func TestRulePriorityRangeEnforced(t *testing.T) {
	e, _ := newEngine(t)
	ctx := context.Background()

	// Below range.
	_, _, err := e.Create(ctx, "cli", "", plan.DesiredState{
		Rules: []kernel.RuleEntry{{Priority: 9999, Fwmark: 1, Table: "wg_tunnel"}},
	})
	if err == nil || !strings.Contains(err.Error(), "outside agent range") {
		t.Errorf("low prio: %v", err)
	}

	// Above range.
	_, _, err = e.Create(ctx, "cli", "", plan.DesiredState{
		Rules: []kernel.RuleEntry{{Priority: 20001, Fwmark: 1, Table: "wg_tunnel"}},
	})
	if err == nil || !strings.Contains(err.Error(), "outside agent range") {
		t.Errorf("high prio: %v", err)
	}
}

func TestRouteOwnershipEnforced(t *testing.T) {
	e, _ := newEngine(t)
	ctx := context.Background()

	// No marks registered → no table is owned.
	_, _, err := e.Create(ctx, "cli", "", plan.DesiredState{
		Routes: []kernel.RouteEntry{{Table: "wg_tunnel", Dest: "default", Dev: "lo"}},
	})
	if err == nil || !strings.Contains(err.Error(), "no mark references") {
		t.Errorf("want 'no mark references', got: %v", err)
	}

	// Forbidden table must be rejected even if a mark seeded it.
	_ = e.Store
	// Attempt to seed main as owned — validateRouteOwnership should still reject.
	seedAllowedTable(t, e, "wg_tunnel", 0x1) // register wg_tunnel so the other check doesn't fire first
	_, _, err = e.Create(ctx, "cli", "", plan.DesiredState{
		Routes: []kernel.RouteEntry{{Table: "main", Dest: "default", Dev: "lo"}},
	})
	if err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Errorf("want 'forbidden', got: %v", err)
	}
}

func TestMixedResourcesApplyTogether(t *testing.T) {
	e, k := newEngine(t)
	seedAllowedTable(t, e, "wg_tunnel", 0x1)
	ctx := context.Background()

	desired := plan.DesiredState{
		IPSets: []plan.IPSetSpec{{Name: "direct", Entries: []string{"77.88.0.0/16"}}},
		Rules:  []kernel.RuleEntry{{Priority: 10500, Fwmark: 0x1, Table: "wg_tunnel"}},
		Routes: []kernel.RouteEntry{{Table: "wg_tunnel", Dest: "default", Dev: "lo"}},
	}
	p, diff, err := e.Create(ctx, "cli", "", desired)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if diff.Empty() {
		t.Error("diff should not be empty")
	}

	if _, err := e.Apply(ctx, p.ID, 30); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// All three resources landed.
	if entries, _ := k.IPSetList("direct"); len(entries) != 1 {
		t.Errorf("direct=%v", entries)
	}
	if rules, _ := k.RuleList(); len(rules) != 1 {
		t.Errorf("rules=%v", rules)
	}
	if routes, _ := k.RouteList("wg_tunnel"); len(routes) != 1 {
		t.Errorf("routes=%v", routes)
	}

	if _, err := e.Revert(ctx, p.ID); err != nil {
		t.Fatalf("revert: %v", err)
	}
	// All three reverted.
	if _, err := k.IPSetList("direct"); !errors.Is(err, kernel.ErrIPSetNotFound) {
		t.Error("direct should be destroyed")
	}
	if rules, _ := k.RuleList(); len(rules) != 0 {
		t.Errorf("rules after revert=%v", rules)
	}
	if routes, _ := k.RouteList("wg_tunnel"); len(routes) != 0 {
		t.Errorf("routes after revert=%v", routes)
	}
}

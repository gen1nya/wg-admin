//go:build integration

// Integration tests for the plan.Engine against a real kernel inside a
// throwaway network namespace. Verifies the full pipeline — create, apply,
// confirm, revert, recover — works end-to-end with actual ip/nft commands.
//
// ipset is host-global even from inside a netns. Tests that touch ipset
// use unique names prefixed "pit_<pid>_<ts>" and register destroy-on-cleanup
// to keep host state clean even if a test is interrupted.
//
//   sudo go test -tags integration ./internal/plan/...
//
// Requires root or passwordless sudo; skipped otherwise.
package plan_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gen1nya/wg-admin/agent/internal/kernel"
	"github.com/gen1nya/wg-admin/agent/internal/model"
	"github.com/gen1nya/wg-admin/agent/internal/plan"
	"github.com/gen1nya/wg-admin/agent/internal/store"
)

func sudoPfx() []string {
	if os.Geteuid() == 0 {
		return nil
	}
	return []string{"sudo", "-n"}
}

func skipNoRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		return
	}
	if err := exec.Command("sudo", "-n", "true").Run(); err != nil {
		t.Skip("needs root or passwordless sudo")
	}
}

type itNS struct {
	name string
	t    *testing.T
}

func newItNS(t *testing.T) *itNS {
	t.Helper()
	name := fmt.Sprintf("plan-it-%d-%d", os.Getpid(), time.Now().UnixNano()%1_000_000)
	argv := append(sudoPfx(), "ip", "netns", "add", name)
	if out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil {
		t.Fatalf("netns add: %v\n%s", err, out)
	}
	ns := &itNS{name: name, t: t}
	t.Cleanup(func() {
		argv := append(sudoPfx(), "ip", "netns", "del", name)
		_ = exec.Command(argv[0], argv[1:]...).Run()
	})
	return ns
}

func (n *itNS) exec(args ...string) {
	n.t.Helper()
	argv := append(sudoPfx(), "ip", "netns", "exec", n.name)
	argv = append(argv, args...)
	if out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil {
		n.t.Fatalf("netns exec %v: %v\n%s", args, err, out)
	}
}

// newITEngine spins up everything the engine needs: netns with lo up,
// SQLite store, real kernel wired into the netns, seeded mark for ownership.
func newITEngine(t *testing.T) (*plan.Engine, *kernel.Real, *itNS, *store.Store) {
	t.Helper()
	skipNoRoot(t)
	ns := newItNS(t)
	ns.exec("ip", "link", "set", "lo", "up")

	dbPath := filepath.Join(t.TempDir(), "plan-it.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// Register mark so routing_table "999" is in the ownership allowlist.
	if _, err := st.UpsertMark(context.Background(), &model.Mark{
		Fwmark: 0x1, Name: "it-tunnel", RoutingTable: "999",
	}); err != nil {
		t.Fatalf("upsert mark: %v", err)
	}

	k := &kernel.Real{
		WgPath:     "wg",
		IPPath:     "ip",
		IPSetPath:  "ipset",
		NFTPath:    "nft",
		Timeout:    10 * time.Second,
		Netns:      ns.name,
		SudoPrefix: sudoPfx(),
	}
	eng := plan.NewEngine(st, k)
	return eng, k, ns, st
}

// uniqueIPSetName returns a name unlikely to collide with other runs.
// Caller schedules destroy in t.Cleanup.
func uniqueIPSetName(t *testing.T, suffix string) string {
	t.Helper()
	name := fmt.Sprintf("pit%d_%s", os.Getpid(), suffix)
	t.Cleanup(func() {
		argv := append(sudoPfx(), "ipset", "destroy", name)
		_ = exec.Command(argv[0], argv[1:]...).Run()
	})
	return name
}

func TestRealPlanFullCycleApplyConfirm(t *testing.T) {
	eng, k, _, _ := newITEngine(t)
	ctx := context.Background()
	if _, err := exec.LookPath("ipset"); err != nil {
		t.Skip("ipset binary unavailable")
	}

	setName := uniqueIPSetName(t, "direct")

	desired := plan.DesiredState{
		IPSets: []plan.IPSetSpec{
			{Name: setName, Entries: []string{"77.88.0.0/16", "87.250.0.0/16"}},
		},
		Rules: []kernel.RuleEntry{
			{Priority: 11000, Fwmark: 0x1, Table: "999"},
		},
		Routes: []kernel.RouteEntry{
			{Table: "999", Dest: "10.77.0.0/24", Dev: "lo"},
		},
		NFT: &plan.NFTSpec{
			Body: `chain forward {
	type filter hook forward priority 0; policy accept;
}`,
		},
	}
	p, diff, err := eng.Create(ctx, "it", "", desired)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if diff.Empty() {
		t.Error("diff should not be empty on fresh kernel")
	}

	if _, err := eng.Apply(ctx, p.ID, 60); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Verify each resource by reading live kernel state.
	entries, err := k.IPSetList(setName)
	if err != nil {
		t.Fatalf("IPSetList after apply: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("ipset entries=%v", entries)
	}
	rules, _ := k.RuleList()
	found := false
	for _, r := range rules {
		if r.Priority == 11000 && r.Fwmark == 1 && r.Table == "999" {
			found = true
		}
	}
	if !found {
		t.Errorf("rule 11000 not found: %+v", rules)
	}
	routes, _ := k.RouteList("999")
	if len(routes) != 1 || routes[0].Dest != "10.77.0.0/24" {
		t.Errorf("routes: %+v", routes)
	}
	got, _ := k.NFTList(plan.AgentNFTTable)
	if !strings.Contains(got, "chain forward") {
		t.Errorf("nft missing chain forward:\n%s", got)
	}

	// Confirm locks it.
	confirmed, err := eng.Confirm(ctx, p.ID)
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if confirmed.State != model.PlanConfirmed {
		t.Errorf("state=%q", confirmed.State)
	}
}

// TestRealPlanIPSetRevertDestroysCreated: created-from-scratch ipset must
// be destroyed on revert, not just emptied.
func TestRealPlanIPSetRevertDestroysCreated(t *testing.T) {
	eng, k, _, _ := newITEngine(t)
	ctx := context.Background()
	if _, err := exec.LookPath("ipset"); err != nil {
		t.Skip("ipset binary unavailable")
	}

	setName := uniqueIPSetName(t, "fresh")
	desired := plan.DesiredState{
		IPSets: []plan.IPSetSpec{
			{Name: setName, Entries: []string{"1.2.3.0/24"}},
		},
	}
	p, _, err := eng.Create(ctx, "it", "", desired)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := eng.Apply(ctx, p.ID, 60); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// Set exists now.
	if _, err := k.IPSetList(setName); err != nil {
		t.Fatalf("ipset not created: %v", err)
	}
	if _, err := eng.Revert(ctx, p.ID); err != nil {
		t.Fatalf("revert: %v", err)
	}
	// Set destroyed.
	if _, err := k.IPSetList(setName); !errors.Is(err, kernel.ErrIPSetNotFound) {
		t.Errorf("set should be destroyed after revert; got %v", err)
	}
}

func TestRealPlanRevertRestoresKernel(t *testing.T) {
	eng, k, _, _ := newITEngine(t)
	ctx := context.Background()

	desired := plan.DesiredState{
		Rules: []kernel.RuleEntry{
			{Priority: 11001, Fwmark: 0x1, Table: "999"},
		},
		Routes: []kernel.RouteEntry{
			{Table: "999", Dest: "10.78.0.0/24", Dev: "lo"},
		},
		NFT: &plan.NFTSpec{
			Body: `chain input {
	type filter hook input priority 0; policy accept;
}`,
		},
	}
	p, _, err := eng.Create(ctx, "it", "", desired)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := eng.Apply(ctx, p.ID, 60); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := eng.Revert(ctx, p.ID); err != nil {
		t.Fatalf("revert: %v", err)
	}

	// Everything should be gone (snapshot said "didn't exist before").
	rules, _ := k.RuleList()
	for _, r := range rules {
		if r.Priority == 11001 {
			t.Errorf("rule still present: %+v", r)
		}
	}
	routes, _ := k.RouteList("999")
	for _, rt := range routes {
		if rt.Dest == "10.78.0.0/24" {
			t.Errorf("route still present: %+v", rt)
		}
	}
	got, _ := k.NFTList(plan.AgentNFTTable)
	if got != "" {
		t.Errorf("nft table should be gone:\n%s", got)
	}
}

// TestRealPlanRevertPreservesPreExistingState: if a resource existed before
// apply, revert must restore it — not just delete.
func TestRealPlanRevertPreservesPreExistingState(t *testing.T) {
	eng, k, ns, _ := newITEngine(t)
	ctx := context.Background()

	// Pre-seed: a route that predates the plan.
	ns.exec("ip", "route", "add", "10.88.0.0/24", "dev", "lo", "table", "999")
	// Pre-seed: an nft table with content.
	preNFT := fmt.Sprintf(`table inet %s {
	chain existing {
		type filter hook input priority 0; policy drop;
	}
}
`, plan.AgentNFTTable)
	if err := k.NFTApply(preNFT); err != nil {
		t.Fatalf("pre-seed nft: %v", err)
	}

	// Plan overwrites the same route + nft table.
	desired := plan.DesiredState{
		Routes: []kernel.RouteEntry{
			{Table: "999", Dest: "10.88.0.0/24", Dev: "lo"}, // same dest
		},
		NFT: &plan.NFTSpec{
			Body: `chain input {
	type filter hook input priority 0; policy accept;
}`,
		},
	}
	p, _, err := eng.Create(ctx, "it", "", desired)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := eng.Apply(ctx, p.ID, 60); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Verify the plan is applied: nft has policy=accept now.
	got, _ := k.NFTList(plan.AgentNFTTable)
	if !strings.Contains(got, "policy accept") {
		t.Errorf("apply didn't take: %s", got)
	}

	// Revert — pre-existing state must be restored.
	if _, err := eng.Revert(ctx, p.ID); err != nil {
		t.Fatalf("revert: %v", err)
	}

	// Route still there.
	routes, _ := k.RouteList("999")
	foundRoute := false
	for _, r := range routes {
		if r.Dest == "10.88.0.0/24" {
			foundRoute = true
		}
	}
	if !foundRoute {
		t.Errorf("pre-existing route lost after revert: %+v", routes)
	}
	// NFT rolled back to policy=drop.
	got, _ = k.NFTList(plan.AgentNFTTable)
	if !strings.Contains(got, "policy drop") {
		t.Errorf("nft not restored to pre-state:\n%s", got)
	}
	if strings.Contains(got, "policy accept") {
		t.Errorf("plan content still present after revert:\n%s", got)
	}
}

// TestRealPlanRecoverExpired: simulate crash — new Engine instance sees
// a stale 'applied' plan with past deadline and auto-reverts it.
func TestRealPlanRecoverExpired(t *testing.T) {
	eng, k, _, st := newITEngine(t)
	ctx := context.Background()

	// Pin time to a known instant so applied_at is deterministic.
	t0 := time.Unix(1_700_000_000, 0)
	eng.Now = func() time.Time { return t0 }

	desired := plan.DesiredState{
		Routes: []kernel.RouteEntry{
			{Table: "999", Dest: "10.99.0.0/24", Dev: "lo"},
		},
	}
	p, _, err := eng.Create(ctx, "it", "", desired)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := eng.Apply(ctx, p.ID, 30); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Drop the first engine's watchdog timers without calling Revert.
	// Simulates a crash: DB has state=applied, no in-process timer.
	// Spin up a fresh engine sharing the same store + kernel, advance clock
	// past the deadline, and let Recover do its job.
	eng2 := plan.NewEngine(st, k)
	eng2.Now = func() time.Time { return t0.Add(60 * time.Second) }
	if err := eng2.Recover(ctx); err != nil {
		t.Fatalf("recover: %v", err)
	}

	// Kernel: route should be gone.
	routes, _ := k.RouteList("999")
	for _, r := range routes {
		if r.Dest == "10.99.0.0/24" {
			t.Errorf("expired plan's route still present: %+v", r)
		}
	}
	// DB: plan state should be 'expired'.
	final, _ := st.GetPlan(ctx, p.ID)
	if final.State != model.PlanExpired {
		t.Errorf("state=%q, want expired", final.State)
	}
}

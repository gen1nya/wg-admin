package plan

import (
	"context"
	"fmt"

	"github.com/gen1nya/wg-admin/agent/internal/kernel"
	"github.com/gen1nya/wg-admin/agent/internal/store"
)

// Ownership rules for tier-2 resources.
//
// Routes: agent manages routes only in tables listed in marks.routing_table,
//         excluding main/local/default (by name or numeric id).
// Rules:  agent manages rules with priority in [RulePriorityMin, RulePriorityMax].
//         Anything outside is invisible to plan apply/revert and won't be
//         touched even if a plan asks for it (rejected).
// NFT:    agent owns exactly one table, `inet <AgentNFTTable>`. Other tables
//         on the host are never touched.
const (
	RulePriorityMin = 10000
	RulePriorityMax = 19999

	// AgentNFTTable is the single nft table name the agent manages.
	AgentNFTTable = "wg-admin"
)

// forbiddenTables are never owned by the agent, even if marks.routing_table
// says otherwise. Includes both named and numeric aliases.
var forbiddenTables = map[string]bool{
	"main":    true,
	"local":   true,
	"default": true,
	"0":       true,
	"253":     true,
	"254":     true,
	"255":     true,
}

// allowedTables returns the set of routing-table names the agent may touch.
// Derived from marks.routing_table minus the forbidden set.
func allowedTables(ctx context.Context, st *store.Store) (map[string]bool, error) {
	marks, err := st.ListMarks(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, m := range marks {
		if forbiddenTables[m.RoutingTable] {
			continue
		}
		out[m.RoutingTable] = true
	}
	return out, nil
}

// validateRouteOwnership: every desired route targets an owned table.
func validateRouteOwnership(desired []kernel.RouteEntry, owned map[string]bool) error {
	for _, r := range desired {
		if forbiddenTables[r.Table] {
			return fmt.Errorf("route in table %q is forbidden (reserved by host)", r.Table)
		}
		if !owned[r.Table] {
			return fmt.Errorf("route in table %q: no mark references this table; register a mark first", r.Table)
		}
	}
	return nil
}

// validateRuleOwnership: every desired rule is in the agent's priority range.
func validateRuleOwnership(desired []kernel.RuleEntry) error {
	seen := map[int]bool{}
	for _, r := range desired {
		if r.Priority < RulePriorityMin || r.Priority > RulePriorityMax {
			return fmt.Errorf("rule priority %d outside agent range [%d..%d]", r.Priority, RulePriorityMin, RulePriorityMax)
		}
		if seen[r.Priority] {
			return fmt.Errorf("duplicate rule priority %d in desired state", r.Priority)
		}
		seen[r.Priority] = true
		if forbiddenTables[r.Table] {
			return fmt.Errorf("rule → table %q is forbidden", r.Table)
		}
	}
	return nil
}

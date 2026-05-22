// Package plan implements the tier-2 plan/apply/confirm flow.
//
// Stateless: the client POSTs the full desired state, agent diffs against
// live kernel, persists the target + snapshot, and applies under a watchdog
// that auto-reverts if not confirmed within the deadline.
//
// Phase 1: ipsets. Phase 2: routes + rules. Phase 3: nft (coming).
package plan

import (
	"github.com/gen1nya/wg-admin/agent/internal/kernel"
)

// DesiredState is the target the client wants to reach. Only resources listed
// here are touched; anything else on the host is left alone. A caller who
// wants strict declarative semantics can later ask for it via a flag.
type DesiredState struct {
	IPSets []IPSetSpec          `json:"ipsets,omitempty"`
	Routes []kernel.RouteEntry  `json:"routes,omitempty"`
	Rules  []kernel.RuleEntry   `json:"rules,omitempty"`
	NFT    *NFTSpec             `json:"nft,omitempty"`
}

// NFTSpec carries the desired body for the agent's single owned nft table.
// The body is everything that goes inside `table inet wg-admin { ... }` —
// chains, rules, sets. Agent wraps it into a full delete-and-redefine
// transaction at apply time; caller must not include `table`, `delete`,
// `flush` or `include` tokens (checked before apply).
type NFTSpec struct {
	Body string `json:"body"`
}

type IPSetSpec struct {
	Name    string   `json:"name"`
	Entries []string `json:"entries"`
}

// Snapshot is the pre-apply live state of everything the plan targets.
// Per-resource: Existed=false means "didn't exist" — revert destroys it.
type Snapshot struct {
	IPSets []IPSetSnapshot `json:"ipsets,omitempty"`
	Routes []RouteSnapshot `json:"routes,omitempty"`
	Rules  []RuleSnapshot  `json:"rules,omitempty"`
	NFT    *NFTSnapshot    `json:"nft,omitempty"`
}

// NFTSnapshot captures the current full `nft list table inet wg-admin`.
// An empty Ruleset with Existed=false means the table didn't exist before —
// revert will delete the table instead of re-creating.
type NFTSnapshot struct {
	Existed bool   `json:"existed"`
	Ruleset string `json:"ruleset,omitempty"` // full `nft list table` output if Existed
}

type IPSetSnapshot struct {
	Name    string   `json:"name"`
	Existed bool     `json:"existed"`
	Entries []string `json:"entries,omitempty"` // valid only if existed
}

type RouteSnapshot struct {
	Table   string             `json:"table"`
	Dest    string             `json:"dest"`
	Existed bool               `json:"existed"`
	Entry   *kernel.RouteEntry `json:"entry,omitempty"` // populated iff Existed
}

type RuleSnapshot struct {
	Priority int               `json:"priority"`
	Existed  bool              `json:"existed"`
	Entry    *kernel.RuleEntry `json:"entry,omitempty"` // populated iff Existed
}

// Diff is the human-readable summary for UI. Not authoritative for apply.
type Diff struct {
	IPSets []IPSetDiff `json:"ipsets,omitempty"`
	Routes []RouteDiff `json:"routes,omitempty"`
	Rules  []RuleDiff  `json:"rules,omitempty"`
	NFT    *NFTDiff    `json:"nft,omitempty"`
}

// NFTDiff describes what happens to the wg-admin table: create, replace, or
// noop. We don't diff rule-by-rule — nft's native atomicity lets us treat
// the whole table as one opaque unit.
type NFTDiff struct {
	Op string `json:"op"` // "create" | "replace" | "noop"
}

type IPSetDiff struct {
	Name    string   `json:"name"`
	Created bool     `json:"created,omitempty"`
	Add     []string `json:"add,omitempty"`
	Remove  []string `json:"remove,omitempty"`
}

type RouteDiff struct {
	Table string `json:"table"`
	Dest  string `json:"dest"`
	Op    string `json:"op"` // "create" | "update" | "noop"
}

type RuleDiff struct {
	Priority int    `json:"priority"`
	Op       string `json:"op"`
}

// Empty reports whether the diff is a no-op.
func (d Diff) Empty() bool {
	for _, s := range d.IPSets {
		if s.Created || len(s.Add) > 0 || len(s.Remove) > 0 {
			return false
		}
	}
	for _, r := range d.Routes {
		if r.Op != "noop" {
			return false
		}
	}
	for _, r := range d.Rules {
		if r.Op != "noop" {
			return false
		}
	}
	if d.NFT != nil && d.NFT.Op != "noop" {
		return false
	}
	return true
}

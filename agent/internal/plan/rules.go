package plan

import (
	"fmt"

	"github.com/gen1nya/wg-admin/agent/internal/kernel"
)

// snapshotRules captures per-priority state for each desired rule. Other
// fwmark rules (outside the priority set) are ignored.
func snapshotRules(k kernel.Kernel, desired []kernel.RuleEntry) ([]RuleSnapshot, error) {
	live, err := k.RuleList()
	if err != nil {
		return nil, fmt.Errorf("rule snapshot: %w", err)
	}
	byPrio := map[int]kernel.RuleEntry{}
	for _, r := range live {
		byPrio[r.Priority] = r
	}
	out := make([]RuleSnapshot, 0, len(desired))
	for _, d := range desired {
		ex, ok := byPrio[d.Priority]
		if !ok {
			out = append(out, RuleSnapshot{Priority: d.Priority, Existed: false})
			continue
		}
		e := ex
		out = append(out, RuleSnapshot{Priority: d.Priority, Existed: true, Entry: &e})
	}
	return out, nil
}

func diffRules(desired []kernel.RuleEntry, snap []RuleSnapshot) []RuleDiff {
	byPrio := map[int]RuleSnapshot{}
	for _, s := range snap {
		byPrio[s.Priority] = s
	}
	out := make([]RuleDiff, 0, len(desired))
	for _, d := range desired {
		s := byPrio[d.Priority]
		op := "create"
		if s.Existed {
			if s.Entry != nil && s.Entry.Fwmark == d.Fwmark && s.Entry.Table == d.Table {
				op = "noop"
			} else {
				op = "update"
			}
		}
		out = append(out, RuleDiff{Priority: d.Priority, Op: op})
	}
	return out
}

// applyRules pushes each desired rule. A pre-existing rule at the same
// priority is first deleted (ip rule add can create duplicates otherwise).
// On failure, rollback restores applied rules from snapshot.
func applyRules(k kernel.Kernel, desired []kernel.RuleEntry, snap []RuleSnapshot) error {
	byPrio := map[int]RuleSnapshot{}
	for _, s := range snap {
		byPrio[s.Priority] = s
	}
	applied := make([]kernel.RuleEntry, 0, len(desired))
	for _, d := range desired {
		s := byPrio[d.Priority]
		if s.Existed && s.Entry != nil {
			// Skip work if identical content.
			if s.Entry.Fwmark == d.Fwmark && s.Entry.Table == d.Table {
				applied = append(applied, d)
				continue
			}
			if err := k.RuleDel(d.Priority); err != nil {
				rollback(k, applied, byPrio)
				return fmt.Errorf("rule del for replace prio=%d: %w", d.Priority, err)
			}
		}
		if err := k.RuleAdd(d); err != nil {
			rollback(k, applied, byPrio)
			return fmt.Errorf("rule add prio=%d: %w", d.Priority, err)
		}
		applied = append(applied, d)
	}
	return nil
}

func rollback(k kernel.Kernel, applied []kernel.RuleEntry, byPrio map[int]RuleSnapshot) {
	for _, r := range applied {
		s := byPrio[r.Priority]
		_ = k.RuleDel(r.Priority)
		if s.Existed && s.Entry != nil {
			_ = k.RuleAdd(*s.Entry)
		}
	}
}

// revertAllRules replays the full snapshot (for manual revert / watchdog).
func revertAllRules(k kernel.Kernel, snap []RuleSnapshot) error {
	var firstErr error
	for _, s := range snap {
		_ = k.RuleDel(s.Priority)
		if s.Existed && s.Entry != nil {
			if err := k.RuleAdd(*s.Entry); err != nil && firstErr == nil {
				firstErr = fmt.Errorf("revert rule prio=%d: %w", s.Priority, err)
			}
		}
	}
	return firstErr
}

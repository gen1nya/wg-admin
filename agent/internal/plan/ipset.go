package plan

import (
	"errors"
	"fmt"
	"sort"

	"github.com/gen1nya/wg-admin/agent/internal/kernel"
)

// snapshotIPSets reads the current entries of each requested set. For a set
// that doesn't exist, Existed=false, Entries=nil.
func snapshotIPSets(k kernel.Kernel, specs []IPSetSpec) ([]IPSetSnapshot, error) {
	out := make([]IPSetSnapshot, 0, len(specs))
	for _, spec := range specs {
		entries, err := k.IPSetList(spec.Name)
		if err != nil {
			if errors.Is(err, kernel.ErrIPSetNotFound) {
				out = append(out, IPSetSnapshot{Name: spec.Name, Existed: false})
				continue
			}
			return nil, fmt.Errorf("snapshot %q: %w", spec.Name, err)
		}
		out = append(out, IPSetSnapshot{
			Name:    spec.Name,
			Existed: true,
			Entries: entries,
		})
	}
	return out, nil
}

// diffIPSets derives the UI-friendly add/remove summary.
func diffIPSets(desired []IPSetSpec, snap []IPSetSnapshot) []IPSetDiff {
	snapByName := map[string]IPSetSnapshot{}
	for _, s := range snap {
		snapByName[s.Name] = s
	}
	out := make([]IPSetDiff, 0, len(desired))
	for _, d := range desired {
		prev := snapByName[d.Name]
		var add, remove []string
		if !prev.Existed {
			add = append(add, d.Entries...)
		} else {
			have := toSet(prev.Entries)
			want := toSet(d.Entries)
			for _, e := range d.Entries {
				if !have[e] {
					add = append(add, e)
				}
			}
			for _, e := range prev.Entries {
				if !want[e] {
					remove = append(remove, e)
				}
			}
		}
		sort.Strings(add)
		sort.Strings(remove)
		out = append(out, IPSetDiff{
			Name:    d.Name,
			Created: !prev.Existed,
			Add:     add,
			Remove:  remove,
		})
	}
	return out
}

// applyIPSets pushes desired.IPSets to the kernel. On any failure it rolls
// back using the per-item snapshot of successfully-applied items.
// Failed item isn't rolled back (IPSetReplace is atomic: failure = no change).
func applyIPSets(k kernel.Kernel, desired []IPSetSpec, snap []IPSetSnapshot) error {
	snapByName := map[string]IPSetSnapshot{}
	for _, s := range snap {
		snapByName[s.Name] = s
	}
	applied := make([]string, 0, len(desired))
	for _, d := range desired {
		if err := k.IPSetReplace(d.Name, d.Entries); err != nil {
			// Roll back already-applied sets to their snapshot state.
			rb := revertIPSets(k, applied, snapByName)
			if rb != nil {
				return fmt.Errorf("apply %q failed (%w); rollback also failed: %v", d.Name, err, rb)
			}
			return fmt.Errorf("apply %q: %w", d.Name, err)
		}
		applied = append(applied, d.Name)
	}
	return nil
}

// revertIPSets restores the given set names to their snapshot state.
// For a set that didn't exist pre-apply, destroy it.
func revertIPSets(k kernel.Kernel, names []string, snapByName map[string]IPSetSnapshot) error {
	var firstErr error
	for _, name := range names {
		prev, ok := snapByName[name]
		if !ok {
			// No snapshot for this set — leave alone. Shouldn't happen in practice.
			continue
		}
		var err error
		if prev.Existed {
			err = k.IPSetReplace(name, prev.Entries)
		} else {
			err = k.IPSetDestroy(name)
		}
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("revert %q: %w", name, err)
		}
	}
	return firstErr
}

// revertAll restores the full snapshot (used by watchdog / manual revert).
func revertAllIPSets(k kernel.Kernel, snap []IPSetSnapshot) error {
	snapByName := map[string]IPSetSnapshot{}
	names := make([]string, 0, len(snap))
	for _, s := range snap {
		snapByName[s.Name] = s
		names = append(names, s.Name)
	}
	return revertIPSets(k, names, snapByName)
}

func toSet(xs []string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

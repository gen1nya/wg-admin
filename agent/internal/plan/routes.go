package plan

import (
	"fmt"

	"github.com/gen1nya/wg-admin/agent/internal/kernel"
)

// snapshotRoutes captures per-dest state of each desired (table, dest) pair.
// Routes not mentioned in desired are ignored (soft semantics).
func snapshotRoutes(k kernel.Kernel, desired []kernel.RouteEntry) ([]RouteSnapshot, error) {
	// Index live routes by (table, dest) once per table.
	byTable := map[string]map[string]kernel.RouteEntry{}
	for _, d := range desired {
		if _, ok := byTable[d.Table]; ok {
			continue
		}
		live, err := k.RouteList(d.Table)
		if err != nil {
			return nil, fmt.Errorf("route snapshot %q: %w", d.Table, err)
		}
		idx := map[string]kernel.RouteEntry{}
		for _, l := range live {
			idx[l.Dest] = l
		}
		byTable[d.Table] = idx
	}

	out := make([]RouteSnapshot, 0, len(desired))
	for _, d := range desired {
		existing, ok := byTable[d.Table][d.Dest]
		if !ok {
			out = append(out, RouteSnapshot{Table: d.Table, Dest: d.Dest, Existed: false})
			continue
		}
		e := existing
		out = append(out, RouteSnapshot{Table: d.Table, Dest: d.Dest, Existed: true, Entry: &e})
	}
	return out, nil
}

func diffRoutes(desired []kernel.RouteEntry, snap []RouteSnapshot) []RouteDiff {
	snapKey := map[string]RouteSnapshot{}
	for _, s := range snap {
		snapKey[s.Table+"\x00"+s.Dest] = s
	}
	out := make([]RouteDiff, 0, len(desired))
	for _, d := range desired {
		key := d.Table + "\x00" + d.Dest
		s := snapKey[key]
		op := "create"
		if s.Existed {
			if s.Entry != nil && s.Entry.Dev == d.Dev && s.Entry.Via == d.Via {
				op = "noop"
			} else {
				op = "update"
			}
		}
		out = append(out, RouteDiff{Table: d.Table, Dest: d.Dest, Op: op})
	}
	return out
}

// applyRoutes pushes each desired route. On any failure, rolls back
// already-applied routes from the snapshot.
func applyRoutes(k kernel.Kernel, desired []kernel.RouteEntry, snap []RouteSnapshot) error {
	snapKey := map[string]RouteSnapshot{}
	for _, s := range snap {
		snapKey[s.Table+"\x00"+s.Dest] = s
	}
	applied := make([]kernel.RouteEntry, 0, len(desired))
	for _, d := range desired {
		if err := k.RouteReplace(d); err != nil {
			if rb := revertRoutes(k, applied, snapKey); rb != nil {
				return fmt.Errorf("apply route %v failed (%w); rollback also failed: %v", d, err, rb)
			}
			return fmt.Errorf("apply route %v: %w", d, err)
		}
		applied = append(applied, d)
	}
	return nil
}

// revertRoutes restores the given list of routes to their snapshot state.
func revertRoutes(k kernel.Kernel, routes []kernel.RouteEntry, snapKey map[string]RouteSnapshot) error {
	var firstErr error
	for _, r := range routes {
		s, ok := snapKey[r.Table+"\x00"+r.Dest]
		if !ok {
			continue
		}
		var err error
		if s.Existed && s.Entry != nil {
			err = k.RouteReplace(*s.Entry)
		} else {
			err = k.RouteDelete(r.Table, r.Dest)
		}
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("revert route %v: %w", r, err)
		}
	}
	return firstErr
}

// revertAllRoutes replays the full snapshot (used by watchdog / manual revert).
func revertAllRoutes(k kernel.Kernel, snap []RouteSnapshot) error {
	var firstErr error
	for _, s := range snap {
		var err error
		if s.Existed && s.Entry != nil {
			err = k.RouteReplace(*s.Entry)
		} else {
			err = k.RouteDelete(s.Table, s.Dest)
		}
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("revert route %s/%s: %w", s.Table, s.Dest, err)
		}
	}
	return firstErr
}

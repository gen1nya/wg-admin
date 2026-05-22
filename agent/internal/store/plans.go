package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/gen1nya/wg-admin/agent/internal/model"
)

const planCols = `id, created_at, created_by, description, desired, diff, state,
	applied_at, confirmed_at, reverted_at, timeout_sec, snapshot_pre`

func scanPlan(row interface{ Scan(...any) error }) (model.Plan, error) {
	var p model.Plan
	var appliedAt, confirmedAt, revertedAt sql.NullInt64
	var snapshotPre sql.NullString
	err := row.Scan(
		&p.ID, &p.CreatedAt, &p.CreatedBy, &p.Description,
		&p.Desired, &p.Diff, &p.State,
		&appliedAt, &confirmedAt, &revertedAt,
		&p.TimeoutSec, &snapshotPre,
	)
	if err != nil {
		return p, err
	}
	if appliedAt.Valid {
		v := appliedAt.Int64
		p.AppliedAt = &v
	}
	if confirmedAt.Valid {
		v := confirmedAt.Int64
		p.ConfirmedAt = &v
	}
	if revertedAt.Valid {
		v := revertedAt.Int64
		p.RevertedAt = &v
	}
	if snapshotPre.Valid {
		p.SnapshotPre = snapshotPre.String
	}
	return p, nil
}

// InsertPlan creates a pending plan. Timeout_sec is set only when the plan
// is applied; leave it 0 at creation.
func (s *Store) InsertPlan(ctx context.Context, p *model.Plan) (int64, error) {
	if p.State == "" {
		p.State = model.PlanPending
	}
	if p.CreatedAt == 0 {
		p.CreatedAt = time.Now().Unix()
	}
	res, err := s.DB.ExecContext(ctx, `
		INSERT INTO plans (created_at, created_by, description,
		                   desired, diff, state, timeout_sec, snapshot_pre)
		VALUES (?,?,?, ?,?,?,?,?)`,
		p.CreatedAt, p.CreatedBy, p.Description,
		p.Desired, p.Diff, p.State, p.TimeoutSec, nullIfEmpty(p.SnapshotPre),
	)
	if err != nil {
		return 0, fmt.Errorf("insert plan: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Store) GetPlan(ctx context.Context, id int64) (model.Plan, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+planCols+` FROM plans WHERE id=?`, id)
	p, err := scanPlan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// ListPlans returns the latest N plans in reverse creation order.
func (s *Store) ListPlans(ctx context.Context, limit int) ([]model.Plan, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+planCols+` FROM plans ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Plan
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// LastConfirmedPlan returns the most recently confirmed plan, or nil if none.
// Boot reconcile reads this to find the live desired state to re-apply.
func (s *Store) LastConfirmedPlan(ctx context.Context) (*model.Plan, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+planCols+` FROM plans
		 WHERE state=? AND confirmed_at IS NOT NULL
		 ORDER BY confirmed_at DESC, id DESC LIMIT 1`,
		model.PlanConfirmed)
	p, err := scanPlan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// ListPlansByState is used by crash recovery to find plans still "applied".
func (s *Store) ListPlansByState(ctx context.Context, state string) ([]model.Plan, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+planCols+` FROM plans WHERE state=? ORDER BY id`, state)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Plan
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// MarkApplied records the apply transition atomically.
// Rejects if current state isn't 'pending' (prevents double-apply).
func (s *Store) MarkApplied(ctx context.Context, id int64, snapshotPre string, timeoutSec int, appliedAt int64) error {
	res, err := s.DB.ExecContext(ctx, `
		UPDATE plans SET state=?, snapshot_pre=?, timeout_sec=?, applied_at=?
		WHERE id=? AND state=?`,
		model.PlanApplied, snapshotPre, timeoutSec, appliedAt, id, model.PlanPending,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("plan %d: not in pending state", id)
	}
	return nil
}

// MarkConfirmed: applied → confirmed.
func (s *Store) MarkConfirmed(ctx context.Context, id int64, confirmedAt int64) error {
	res, err := s.DB.ExecContext(ctx, `
		UPDATE plans SET state=?, confirmed_at=?
		WHERE id=? AND state=?`,
		model.PlanConfirmed, confirmedAt, id, model.PlanApplied,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("plan %d: not in applied state", id)
	}
	return nil
}

// MarkReverted: applied → reverted (manual revert or watchdog).
// finalState can be PlanReverted (manual) or PlanExpired (watchdog timeout).
func (s *Store) MarkReverted(ctx context.Context, id int64, finalState string, revertedAt int64) error {
	if finalState != model.PlanReverted && finalState != model.PlanExpired {
		return fmt.Errorf("bad final state %q", finalState)
	}
	res, err := s.DB.ExecContext(ctx, `
		UPDATE plans SET state=?, reverted_at=?
		WHERE id=? AND state=?`,
		finalState, revertedAt, id, model.PlanApplied,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("plan %d: not in applied state", id)
	}
	return nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

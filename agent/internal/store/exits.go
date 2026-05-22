package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/gen1nya/wg-admin/agent/internal/model"
)

const exitCols = `id, name, kind, out_interface, mark_id, masquerade,
	description, enabled`

func scanExit(row interface{ Scan(...any) error }) (model.Exit, error) {
	var e model.Exit
	err := row.Scan(&e.ID, &e.Name, &e.Kind, &e.OutInterface, &e.MarkID,
		&e.Masquerade, &e.Description, &e.Enabled)
	return e, err
}

func (s *Store) ListExits(ctx context.Context) ([]model.Exit, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+exitCols+` FROM exits ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Exit
	for rows.Next() {
		e, err := scanExit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) GetExit(ctx context.Context, id int64) (model.Exit, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+exitCols+` FROM exits WHERE id=?`, id)
	e, err := scanExit(row)
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	return e, err
}

// UpsertMark inserts or replaces a mark by name. Returns its id.
func (s *Store) UpsertMark(ctx context.Context, m *model.Mark) (int64, error) {
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO marks (fwmark, name, routing_table, description)
		VALUES (?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET
		  fwmark=excluded.fwmark,
		  routing_table=excluded.routing_table,
		  description=excluded.description`,
		m.Fwmark, m.Name, m.RoutingTable, m.Description)
	if err != nil {
		return 0, err
	}
	row := s.DB.QueryRowContext(ctx, `SELECT id FROM marks WHERE name=?`, m.Name)
	var id int64
	if err := row.Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

// UpsertExit inserts or replaces an exit by name. Returns its id.
func (s *Store) UpsertExit(ctx context.Context, e *model.Exit) (int64, error) {
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO exits (name, kind, out_interface, mark_id, masquerade, description, enabled)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET
		  kind=excluded.kind,
		  out_interface=excluded.out_interface,
		  mark_id=excluded.mark_id,
		  masquerade=excluded.masquerade,
		  description=excluded.description,
		  enabled=excluded.enabled`,
		e.Name, e.Kind, e.OutInterface, e.MarkID, e.Masquerade, e.Description, e.Enabled)
	if err != nil {
		return 0, err
	}
	row := s.DB.QueryRowContext(ctx, `SELECT id FROM exits WHERE name=?`, e.Name)
	var id int64
	if err := row.Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Store) ListMarks(ctx context.Context) ([]model.Mark, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, fwmark, name, routing_table, description FROM marks ORDER BY fwmark`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Mark
	for rows.Next() {
		var m model.Mark
		if err := rows.Scan(&m.ID, &m.Fwmark, &m.Name, &m.RoutingTable, &m.Description); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

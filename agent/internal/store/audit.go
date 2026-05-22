package store

import (
	"context"
	"time"

	"github.com/gen1nya/wg-admin/agent/internal/model"
)

// LogAudit appends an entry to audit_log. payload should be a JSON string.
func (s *Store) LogAudit(ctx context.Context, actor, action, entityType string, entityID *int64, payload string) error {
	var eid any
	if entityID != nil {
		eid = *entityID
	}
	if payload == "" {
		payload = "{}"
	}
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO audit_log (ts, actor, action, entity_type, entity_id, payload)
		VALUES (?,?,?,?,?,?)`,
		time.Now().Unix(), actor, action, entityType, eid, payload,
	)
	return err
}

func (s *Store) ListAudit(ctx context.Context, limit int) ([]model.AuditEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, ts, actor, action, entity_type, entity_id, payload
		FROM audit_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.AuditEntry
	for rows.Next() {
		var e model.AuditEntry
		var eid interface{}
		if err := rows.Scan(&e.ID, &e.TS, &e.Actor, &e.Action, &e.EntityType, &eid, &e.Payload); err != nil {
			return nil, err
		}
		if v, ok := eid.(int64); ok {
			e.EntityID = &v
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

package repository

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"service/internal/model"
)

func (r *Repo) InsertAudit(ctx context.Context, actor *uuid.UUID, action, targetType, targetID string, metadata map[string]any, ip string) error {
	if metadata == nil {
		metadata = map[string]any{}
	}
	b, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(ctx, `INSERT INTO audit_logs (actor_user_id, action, target_type, target_id, metadata, ip) VALUES ($1,$2,$3,$4,$5,$6)`,
		actor, action, targetType, targetID, b, ip)
	return err
}

func (r *Repo) ListAudit(ctx context.Context, limit, offset int) ([]*model.AuditLog, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.db.Query(ctx, `SELECT a.id, a.actor_user_id, COALESCE(u.email, ''), a.action, a.target_type, a.target_id, a.metadata, a.ip, a.created_at
		FROM audit_logs a LEFT JOIN users u ON u.id = a.actor_user_id ORDER BY a.created_at DESC, a.id DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*model.AuditLog{}
	for rows.Next() {
		var a model.AuditLog
		var meta []byte
		if err := rows.Scan(&a.ID, &a.ActorUserID, &a.ActorEmail, &a.Action, &a.TargetType, &a.TargetID, &meta, &a.IP, &a.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(meta, &a.Metadata)
		out = append(out, &a)
	}
	return out, rows.Err()
}

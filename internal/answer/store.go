package answer

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sifty-works/sifty-server/internal/db"
)

const (
	RoleUser  = "user"
	RoleModel = "model"
)

type ChatMessage struct {
	Position  int      `json:"position"`
	Role      string   `json:"role"`
	Text      string   `json:"text"`
	Field     string   `json:"field"`
	Proposal  []string `json:"proposal"`
	CreatedAt string   `json:"created_at"`
}

func Messages(ctx context.Context, database *db.DB, form int64) ([]ChatMessage, error) {
	rows, err := database.QueryContext(ctx,
		`SELECT position, role, text, field, proposal, created_at FROM answer_chat WHERE form=? ORDER BY position`, form)
	if err != nil {
		return nil, fmt.Errorf("answer: chat: %w", err)
	}
	defer rows.Close()
	out := []ChatMessage{}
	for rows.Next() {
		var m ChatMessage
		var proposal string
		if err := rows.Scan(&m.Position, &m.Role, &m.Text, &m.Field, &proposal, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("answer: chat: %w", err)
		}
		if err := json.Unmarshal([]byte(proposal), &m.Proposal); err != nil {
			return nil, fmt.Errorf("answer: chat: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func AddExchange(ctx context.Context, database *db.DB, form int64, at time.Time, messages ...ChatMessage) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("answer: chat: %w", err)
	}
	defer tx.Rollback()
	for _, m := range messages {
		proposal, _ := json.Marshal(append([]string{}, m.Proposal...))
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO answer_chat (form, position, role, text, field, proposal, created_at)
			 SELECT ?, COALESCE(MAX(position), 0)+1, ?, ?, ?, ?, ? FROM answer_chat WHERE form=?`,
			form, m.Role, m.Text, m.Field, string(proposal), at.UTC().Format(time.RFC3339), form); err != nil {
			return fmt.Errorf("answer: chat: %w", err)
		}
	}
	return tx.Commit()
}

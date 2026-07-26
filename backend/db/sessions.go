package db

import (
	"database/sql"
	"fmt"
	"time"
)

// Session is a user-facing view of one refresh_tokens row: a single logged-in
// device/browser. Used by the "active sessions" self-service list/revoke API
// so a user can see and sign out their own other devices.
type Session struct {
	ID         int64      `json:"id"`
	DeviceID   string     `json:"device_id,omitempty"`
	UserAgent  string     `json:"user_agent,omitempty"`
	IP         string     `json:"ip,omitempty"`
	Remember   bool       `json:"remember"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  time.Time  `json:"expires_at"`
}

// ListUserSessions returns userID's still-usable sessions (not logged out,
// not expired), most recently used first.
func ListUserSessions(database *sql.DB, userID int64) ([]Session, error) {
	rows, err := database.Query(
		`SELECT id, COALESCE(device_id, ''), COALESCE(user_agent, ''), COALESCE(ip, ''), remember, created_at, last_used_at, expires_at
		 FROM refresh_tokens
		 WHERE user_id = $1 AND used = false AND expires_at > NOW()
		 ORDER BY COALESCE(last_used_at, created_at) DESC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var s Session
		var lastUsedAt sql.NullTime
		if err := rows.Scan(&s.ID, &s.DeviceID, &s.UserAgent, &s.IP, &s.Remember, &s.CreatedAt, &lastUsedAt, &s.ExpiresAt); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		if lastUsedAt.Valid {
			s.LastUsedAt = &lastUsedAt.Time
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
}

// RevokeSession logs out one of userID's own sessions by id. Scoped to
// userID so a user can never revoke someone else's session by guessing an id.
func RevokeSession(database *sql.DB, userID, sessionID int64) error {
	res, err := database.Exec(
		"UPDATE refresh_tokens SET used = true, used_at = NOW() WHERE id = $1 AND user_id = $2 AND used = false",
		sessionID, userID,
	)
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

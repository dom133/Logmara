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
	ID              int64      `json:"id"`
	DeviceID        string     `json:"device_id,omitempty"`
	UserAgent       string     `json:"user_agent,omitempty"`
	IP              string     `json:"ip,omitempty"`
	Remember        bool       `json:"remember"`
	CreatedAt       time.Time  `json:"created_at"`
	LastUsedAt      *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt       time.Time  `json:"expires_at"`
	ScreenResolution string    `json:"screen_resolution,omitempty"`
	Timezone        string     `json:"timezone,omitempty"`
	// JTI is the access-token id issued alongside this refresh token. Used
	// server-side to tell which row is the one actually live in the
	// caller's current cookies (see handler.ListSessions) - never sent to
	// the client, since device_id alone is shared by every session from
	// the same browser and isn't enough to identify "this" one.
	JTI string `json:"-"`
}

// ListUserSessions returns userID's still-usable sessions (not logged out,
// not expired), most recently used first.
func ListUserSessions(database *sql.DB, userID int64) ([]Session, error) {
	rows, err := database.Query(
		`SELECT id, COALESCE(device_id, ''), COALESCE(user_agent, ''), COALESCE(ip, ''), remember, created_at, last_used_at, expires_at, COALESCE(screen_resolution, ''), COALESCE(timezone, ''), COALESCE(jti, '')
		 FROM refresh_tokens
		 WHERE user_id = $1 AND used = false AND expires_at > NOW()
		   AND (replaced_by IS NULL OR replaced_by = '')
		   AND (
		     remember = true
		     OR COALESCE(last_used_at, created_at) > NOW() - (COALESCE((SELECT value FROM app_settings WHERE key = 'session_timeout_min'), '15')::int || ' minutes')::INTERVAL
		   )
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
		if err := rows.Scan(&s.ID, &s.DeviceID, &s.UserAgent, &s.IP, &s.Remember, &s.CreatedAt, &lastUsedAt, &s.ExpiresAt, &s.ScreenResolution, &s.Timezone, &s.JTI); err != nil {
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
// Also blacklists the access token JTI that was issued alongside this
// session's refresh token (if any), so the device holding it is rejected on
// its very next request instead of staying valid until that access token's
// own natural expiry - see GET /auth/session-check, which the frontend
// polls specifically to notice this quickly.
func RevokeSession(database *sql.DB, userID, sessionID int64) error {
	var jti sql.NullString
	err := database.QueryRow(
		"UPDATE refresh_tokens SET used = true, used_at = NOW() WHERE id = $1 AND user_id = $2 AND used = false RETURNING jti",
		sessionID, userID,
	).Scan(&jti)
	if err != nil {
		if err == sql.ErrNoRows {
			return sql.ErrNoRows
		}
		return fmt.Errorf("revoke session: %w", err)
	}
	if jti.Valid && jti.String != "" {
		if err := BlacklistJTI(database, jti.String); err != nil {
			return fmt.Errorf("blacklist revoked session's token: %w", err)
		}
	}
	return nil
}

// InvalidateDeviceSessions marks every still-active refresh token userID
// already has for deviceID as used, and blacklists the access-token JTI
// issued alongside each one. device_id is a long-lived per-browser cookie
// shared by every login from that browser, not a per-login identifier - so
// without this, a fresh password login on a browser that still holds an
// earlier "remember this device" session would leave that old session
// alive in parallel (valid up to its own 60-day expiry) instead of
// replacing it, and My Sessions would accumulate duplicate rows for what
// is really one device. Call this before inserting the new login's own
// refresh token row, or it would immediately invalidate that one too.
func InvalidateDeviceSessions(database *sql.DB, userID int64, deviceID string) error {
	if deviceID == "" {
		return nil
	}
	rows, err := database.Query(
		`UPDATE refresh_tokens SET used = true, used_at = NOW()
		 WHERE user_id = $1 AND device_id = $2 AND used = false
		 RETURNING COALESCE(jti, '')`,
		userID, deviceID,
	)
	if err != nil {
		return fmt.Errorf("invalidate device sessions: %w", err)
	}
	defer rows.Close()

	var jtis []string
	for rows.Next() {
		var jti string
		if err := rows.Scan(&jti); err != nil {
			return fmt.Errorf("scan invalidated session jti: %w", err)
		}
		if jti != "" {
			jtis = append(jtis, jti)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("invalidate device sessions: %w", err)
	}
	rows.Close()

	for _, jti := range jtis {
		if err := BlacklistJTI(database, jti); err != nil {
			return fmt.Errorf("blacklist invalidated session's token: %w", err)
		}
	}
	return nil
}

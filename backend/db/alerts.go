package db

import (
	"database/sql"
	"fmt"
	"os"

	"github.com/lib/pq"

	"syslog-gui/model"
	"syslog-gui/util"
)

func encryptionKey(db *sql.DB) string {
	if k := os.Getenv("ENCRYPTION_KEY"); k != "" {
		return k
	}
	return getSettingRaw(db, "encryption_key")
}

// ---- Alerts ----

func CreateAlert(db *sql.DB, req model.AlertRequest, createdBy int64) (*model.Alert, error) {
	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}
	if req.WindowMinutes <= 0 {
		req.WindowMinutes = 5
	}
	if req.CooldownMinutes <= 0 {
		req.CooldownMinutes = 15
	}

	var id int64
	err := db.QueryRow(
		`INSERT INTO alerts (name, description, rule_type, severity, device_ips, parser_names, message_pattern, threshold, window_minutes, cooldown_minutes, audit_action_filter, is_active, created_by, updated_at)
		 VALUES ($1, $2, $3, NULLIF($4,''), $5, $6, NULLIF($7,''), $8, $9, $10, NULLIF($11,''), $12, $13, NOW())
		 RETURNING id`,
		req.Name, req.Description, req.RuleType, req.Severity, pq.Array(req.DeviceIPs), pq.Array(req.ParserNames), req.MessagePattern,
		req.Threshold, req.WindowMinutes, req.CooldownMinutes, req.AuditActionFilter, isActive, createdBy,
	).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("create alert: %w", err)
	}

	if err := SetAlertChannels(db, id, req.ChannelIDs); err != nil {
		return nil, err
	}
	if err := SetAlertFieldConditions(db, id, req.FieldConditions); err != nil {
		return nil, err
	}

	return GetAlert(db, id)
}

func UpdateAlert(db *sql.DB, id int64, req model.AlertRequest) (*model.Alert, error) {
	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}
	if req.WindowMinutes <= 0 {
		req.WindowMinutes = 5
	}
	if req.CooldownMinutes <= 0 {
		req.CooldownMinutes = 15
	}

	_, err := db.Exec(
		`UPDATE alerts SET name=$1, description=$2, rule_type=$3, severity=NULLIF($4,''), device_ips=$5,
		 parser_names=$6, message_pattern=NULLIF($7,''), threshold=$8, window_minutes=$9, cooldown_minutes=$10,
		 audit_action_filter=NULLIF($11,''), is_active=$12, updated_at=NOW() WHERE id=$13`,
		req.Name, req.Description, req.RuleType, req.Severity, pq.Array(req.DeviceIPs), pq.Array(req.ParserNames), req.MessagePattern,
		req.Threshold, req.WindowMinutes, req.CooldownMinutes, req.AuditActionFilter, isActive, id,
	)
	if err != nil {
		return nil, fmt.Errorf("update alert: %w", err)
	}

	if err := SetAlertChannels(db, id, req.ChannelIDs); err != nil {
		return nil, err
	}
	if err := SetAlertFieldConditions(db, id, req.FieldConditions); err != nil {
		return nil, err
	}

	return GetAlert(db, id)
}

func DeleteAlert(db *sql.DB, id int64) error {
	_, err := db.Exec("DELETE FROM alerts WHERE id=$1", id)
	return err
}

func SetAlertChannels(db *sql.DB, alertID int64, channelIDs []int64) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin alert channels tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM alert_channels WHERE alert_id=$1", alertID); err != nil {
		return fmt.Errorf("clear alert channels: %w", err)
	}
	for _, chID := range channelIDs {
		if _, err := tx.Exec("INSERT INTO alert_channels (alert_id, channel_id) VALUES ($1,$2) ON CONFLICT DO NOTHING", alertID, chID); err != nil {
			return fmt.Errorf("assign channel %d to alert %d: %w", chID, alertID, err)
		}
	}
	return tx.Commit()
}

func scanAlert(row *sql.Rows) (model.Alert, error) {
	var a model.Alert
	var description, severity, messagePattern, auditActionFilter sql.NullString
	var threshold sql.NullInt64
	var createdBy sql.NullInt64
	var lastFiredAt sql.NullTime
	err := row.Scan(&a.ID, &a.Name, &description, &a.RuleType, &severity, pq.Array(&a.DeviceIPs), pq.Array(&a.ParserNames), &messagePattern,
		&threshold, &a.WindowMinutes, &a.CooldownMinutes, &auditActionFilter, &a.IsActive, &createdBy, &a.CreatedAt, &a.UpdatedAt, &lastFiredAt)
	if err != nil {
		return a, err
	}
	a.Description = description.String
	a.Severity = severity.String
	a.MessagePattern = messagePattern.String
	a.Threshold = int(threshold.Int64)
	a.AuditActionFilter = auditActionFilter.String
	if a.DeviceIPs == nil {
		a.DeviceIPs = []string{}
	}
	if a.ParserNames == nil {
		a.ParserNames = []string{}
	}
	if createdBy.Valid {
		a.CreatedBy = &createdBy.Int64
	}
	if lastFiredAt.Valid {
		a.LastFiredAt = &lastFiredAt.Time
	}
	return a, nil
}

const alertColumns = `id, name, description, rule_type, severity, device_ips, parser_names, message_pattern,
	threshold, window_minutes, cooldown_minutes, audit_action_filter, is_active, created_by, created_at, updated_at, last_fired_at`

func GetAllAlerts(db *sql.DB) ([]model.Alert, error) {
	rows, err := db.Query(`SELECT ` + alertColumns + ` FROM alerts ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list alerts: %w", err)
	}
	defer rows.Close()

	var alerts []model.Alert
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, fmt.Errorf("scan alert: %w", err)
		}
		alerts = append(alerts, a)
	}

	if err := attachChannelIDs(db, alerts); err != nil {
		return nil, err
	}
	if err := attachFieldConditions(db, alerts); err != nil {
		return nil, err
	}
	return alerts, nil
}

func GetActiveAlertsByType(db *sql.DB, ruleType string) ([]model.Alert, error) {
	rows, err := db.Query(`SELECT `+alertColumns+` FROM alerts WHERE rule_type=$1 AND is_active=TRUE`, ruleType)
	if err != nil {
		return nil, fmt.Errorf("list active alerts: %w", err)
	}
	defer rows.Close()

	var alerts []model.Alert
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, fmt.Errorf("scan alert: %w", err)
		}
		alerts = append(alerts, a)
	}
	if err := attachFieldConditions(db, alerts); err != nil {
		return nil, err
	}
	return alerts, nil
}

func GetAlert(db *sql.DB, id int64) (*model.Alert, error) {
	rows, err := db.Query(`SELECT `+alertColumns+` FROM alerts WHERE id=$1`, id)
	if err != nil {
		return nil, fmt.Errorf("get alert: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	a, err := scanAlert(rows)
	if err != nil {
		return nil, fmt.Errorf("scan alert: %w", err)
	}

	alerts := []model.Alert{a}
	if err := attachChannelIDs(db, alerts); err != nil {
		return nil, err
	}
	if err := attachFieldConditions(db, alerts); err != nil {
		return nil, err
	}
	return &alerts[0], nil
}

// SetAlertFieldConditions replaces alertID's field conditions with
// conditions, in a single transaction (same delete-then-insert pattern as
// SetAlertChannels).
func SetAlertFieldConditions(db *sql.DB, alertID int64, conditions []model.AlertFieldCondition) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin field conditions tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM alert_field_conditions WHERE alert_id=$1", alertID); err != nil {
		return fmt.Errorf("clear field conditions: %w", err)
	}
	for _, cond := range conditions {
		if cond.FieldName == "" || cond.Value == "" {
			continue
		}
		operator := cond.Operator
		if operator == "" {
			operator = model.FieldOpEquals
		}
		if _, err := tx.Exec(
			"INSERT INTO alert_field_conditions (alert_id, field_name, operator, value) VALUES ($1, $2, $3, $4)",
			alertID, cond.FieldName, operator, cond.Value,
		); err != nil {
			return fmt.Errorf("insert field condition for alert %d: %w", alertID, err)
		}
	}
	return tx.Commit()
}

func attachFieldConditions(db *sql.DB, alerts []model.Alert) error {
	if len(alerts) == 0 {
		return nil
	}
	byID := make(map[int64]*model.Alert, len(alerts))
	for i := range alerts {
		alerts[i].FieldConditions = []model.AlertFieldCondition{}
		byID[alerts[i].ID] = &alerts[i]
	}

	rows, err := db.Query("SELECT id, alert_id, field_name, operator, value FROM alert_field_conditions ORDER BY id")
	if err != nil {
		return fmt.Errorf("list alert field conditions: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var alertID int64
		var cond model.AlertFieldCondition
		if err := rows.Scan(&cond.ID, &alertID, &cond.FieldName, &cond.Operator, &cond.Value); err != nil {
			return fmt.Errorf("scan alert field condition: %w", err)
		}
		if a, ok := byID[alertID]; ok {
			a.FieldConditions = append(a.FieldConditions, cond)
		}
	}
	return nil
}

func attachChannelIDs(db *sql.DB, alerts []model.Alert) error {
	if len(alerts) == 0 {
		return nil
	}
	byID := make(map[int64]*model.Alert, len(alerts))
	for i := range alerts {
		alerts[i].ChannelIDs = []int64{}
		byID[alerts[i].ID] = &alerts[i]
	}

	rows, err := db.Query("SELECT alert_id, channel_id FROM alert_channels")
	if err != nil {
		return fmt.Errorf("list alert channels: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var alertID, channelID int64
		if err := rows.Scan(&alertID, &channelID); err != nil {
			return fmt.Errorf("scan alert channel: %w", err)
		}
		if a, ok := byID[alertID]; ok {
			a.ChannelIDs = append(a.ChannelIDs, channelID)
		}
	}
	return nil
}

func MarkAlertFired(db *sql.DB, id int64) error {
	_, err := db.Exec("UPDATE alerts SET last_fired_at = NOW() WHERE id=$1", id)
	return err
}

// ---- Notification channels ----

func CreateNotificationChannel(database *sql.DB, req model.NotificationChannelRequest) (*model.NotificationChannel, error) {
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	config := req.Config
	if len(config) == 0 {
		config = []byte("{}")
	}

	var secret sql.NullString
	if req.Secret != "" {
		enc, err := util.Encrypt(encryptionKey(database), req.Secret)
		if err != nil {
			return nil, fmt.Errorf("encrypt channel secret: %w", err)
		}
		secret = sql.NullString{String: enc, Valid: true}
	}

	var id int64
	err := database.QueryRow(
		`INSERT INTO notification_channels (name, type, config, secret, enabled, updated_at)
		 VALUES ($1, $2, $3, $4, $5, NOW()) RETURNING id`,
		req.Name, req.Type, []byte(config), secret, enabled,
	).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("create notification channel: %w", err)
	}
	return GetNotificationChannel(database, id)
}

func UpdateNotificationChannel(database *sql.DB, id int64, req model.NotificationChannelRequest) (*model.NotificationChannel, error) {
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	config := req.Config
	if len(config) == 0 {
		config = []byte("{}")
	}

	if req.Secret != "" {
		enc, err := util.Encrypt(encryptionKey(database), req.Secret)
		if err != nil {
			return nil, fmt.Errorf("encrypt channel secret: %w", err)
		}
		_, err = database.Exec(
			`UPDATE notification_channels SET name=$1, type=$2, config=$3, secret=$4, enabled=$5, updated_at=NOW() WHERE id=$6`,
			req.Name, req.Type, []byte(config), enc, enabled, id,
		)
		if err != nil {
			return nil, fmt.Errorf("update notification channel: %w", err)
		}
	} else {
		// Empty secret in the request means "keep the existing one" - the API
		// never returns the decrypted secret, so there is nothing else for a
		// client to resubmit here.
		_, err := database.Exec(
			`UPDATE notification_channels SET name=$1, type=$2, config=$3, enabled=$4, updated_at=NOW() WHERE id=$5`,
			req.Name, req.Type, []byte(config), enabled, id,
		)
		if err != nil {
			return nil, fmt.Errorf("update notification channel: %w", err)
		}
	}
	return GetNotificationChannel(database, id)
}

func DeleteNotificationChannel(db *sql.DB, id int64) error {
	_, err := db.Exec("DELETE FROM notification_channels WHERE id=$1", id)
	return err
}

func scanChannel(row *sql.Rows) (model.NotificationChannel, error) {
	var ch model.NotificationChannel
	var config []byte
	var secret sql.NullString
	if err := row.Scan(&ch.ID, &ch.Name, &ch.Type, &config, &secret, &ch.Enabled, &ch.CreatedAt, &ch.UpdatedAt); err != nil {
		return ch, err
	}
	ch.Config = config
	ch.HasSecret = secret.Valid && secret.String != ""
	return ch, nil
}

const channelColumns = `id, name, type, config, secret, enabled, created_at, updated_at`

func GetAllNotificationChannels(db *sql.DB) ([]model.NotificationChannel, error) {
	rows, err := db.Query(`SELECT ` + channelColumns + ` FROM notification_channels ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list notification channels: %w", err)
	}
	defer rows.Close()

	var channels []model.NotificationChannel
	for rows.Next() {
		ch, err := scanChannel(rows)
		if err != nil {
			return nil, fmt.Errorf("scan notification channel: %w", err)
		}
		channels = append(channels, ch)
	}
	return channels, nil
}

func GetNotificationChannel(db *sql.DB, id int64) (*model.NotificationChannel, error) {
	rows, err := db.Query(`SELECT `+channelColumns+` FROM notification_channels WHERE id=$1`, id)
	if err != nil {
		return nil, fmt.Errorf("get notification channel: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	ch, err := scanChannel(rows)
	if err != nil {
		return nil, fmt.Errorf("scan notification channel: %w", err)
	}
	return &ch, nil
}

// GetChannelsForAlert returns the enabled channels assigned to alertID, for
// internal dispatch use. Secrets are still encrypted here - callers should
// go through DecryptChannelSecret for the one they intend to send through.
func GetChannelsForAlert(database *sql.DB, alertID int64) ([]model.NotificationChannel, error) {
	rows, err := database.Query(
		`SELECT nc.`+channelColumns+` FROM notification_channels nc
		 JOIN alert_channels ac ON ac.channel_id = nc.id
		 WHERE ac.alert_id = $1 AND nc.enabled = TRUE`, alertID)
	if err != nil {
		return nil, fmt.Errorf("list alert channels: %w", err)
	}
	defer rows.Close()

	var channels []model.NotificationChannel
	for rows.Next() {
		ch, err := scanChannel(rows)
		if err != nil {
			return nil, fmt.Errorf("scan notification channel: %w", err)
		}
		channels = append(channels, ch)
	}
	return channels, nil
}

// DecryptChannelSecret decrypts a channel's stored secret using the app's
// encryption key. Returns "" if the channel has no secret set.
func DecryptChannelSecret(database *sql.DB, id int64) (string, error) {
	var secret sql.NullString
	err := database.QueryRow("SELECT secret FROM notification_channels WHERE id=$1", id).Scan(&secret)
	if err != nil {
		return "", err
	}
	if !secret.Valid || secret.String == "" {
		return "", nil
	}
	return util.Decrypt(encryptionKey(database), secret.String)
}

// ---- Notification log ----

func LogNotification(db *sql.DB, entry model.NotificationLogEntry) error {
	_, err := db.Exec(
		`INSERT INTO notification_log (alert_id, alert_name, channel_id, channel_name, channel_type, status, detail)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		entry.AlertID, entry.AlertName, entry.ChannelID, entry.ChannelName, entry.ChannelType, entry.Status, entry.Detail,
	)
	return err
}

func GetNotificationHistory(db *sql.DB, limit int) ([]model.NotificationLogEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := db.Query(
		`SELECT id, alert_id, alert_name, channel_id, channel_name, channel_type, status, detail, created_at
		 FROM notification_log ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list notification history: %w", err)
	}
	defer rows.Close()

	var entries []model.NotificationLogEntry
	for rows.Next() {
		var e model.NotificationLogEntry
		var detail sql.NullString
		if err := rows.Scan(&e.ID, &e.AlertID, &e.AlertName, &e.ChannelID, &e.ChannelName, &e.ChannelType, &e.Status, &detail, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan notification history entry: %w", err)
		}
		e.Detail = detail.String
		entries = append(entries, e)
	}
	return entries, nil
}

func ClearNotificationHistory(db *sql.DB) error {
	_, err := db.Exec("TRUNCATE TABLE notification_log")
	return err
}

// ---- In-app notifications ----

func CreateInAppNotification(db *sql.DB, alertID *int64, title, message, severity string) (int64, error) {
	var id int64
	err := db.QueryRow(
		`INSERT INTO in_app_notifications (alert_id, title, message, severity) VALUES ($1, $2, $3, $4) RETURNING id`,
		alertID, title, message, severity,
	).Scan(&id)
	return id, err
}

func GetInAppNotifications(db *sql.DB, sinceID int64, limit int) ([]model.InAppNotification, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := db.Query(
		`SELECT id, alert_id, title, message, severity, created_at FROM in_app_notifications
		 WHERE id > $1 ORDER BY id DESC LIMIT $2`, sinceID, limit)
	if err != nil {
		return nil, fmt.Errorf("list in-app notifications: %w", err)
	}
	defer rows.Close()

	var items []model.InAppNotification
	for rows.Next() {
		var n model.InAppNotification
		if err := rows.Scan(&n.ID, &n.AlertID, &n.Title, &n.Message, &n.Severity, &n.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan in-app notification: %w", err)
		}
		items = append(items, n)
	}
	return items, nil
}

func GetUnreadNotificationCount(db *sql.DB, userID int64) (count int64, lastID int64, err error) {
	var lastRead int64
	err = db.QueryRow("SELECT last_read_id FROM user_notification_state WHERE user_id=$1", userID).Scan(&lastRead)
	if err != nil && err != sql.ErrNoRows {
		return 0, 0, fmt.Errorf("get notification state: %w", err)
	}

	err = db.QueryRow("SELECT COUNT(*), COALESCE(MAX(id), 0) FROM in_app_notifications WHERE id > $1", lastRead).Scan(&count, &lastID)
	if err != nil {
		return 0, 0, fmt.Errorf("count unread notifications: %w", err)
	}
	if lastID == 0 {
		lastID = lastRead
	}
	return count, lastID, nil
}

// GetLastReadID returns the id boundary userID has marked read (0 if they
// have never read anything), for filtering the notification list down to
// just what's still unread.
func GetLastReadID(db *sql.DB, userID int64) (int64, error) {
	var lastRead int64
	err := db.QueryRow("SELECT last_read_id FROM user_notification_state WHERE user_id=$1", userID).Scan(&lastRead)
	if err != nil && err != sql.ErrNoRows {
		return 0, fmt.Errorf("get notification state: %w", err)
	}
	return lastRead, nil
}

func MarkNotificationsRead(db *sql.DB, userID, lastReadID int64) error {
	_, err := db.Exec(
		`INSERT INTO user_notification_state (user_id, last_read_id) VALUES ($1, $2)
		 ON CONFLICT (user_id) DO UPDATE SET last_read_id = GREATEST(user_notification_state.last_read_id, $2)`,
		userID, lastReadID,
	)
	return err
}

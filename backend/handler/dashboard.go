package handler

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"syslog-gui/model"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/lib/pq"
)

func getUserRole(c *gin.Context) string {
	claims, exists := c.Get("claims")
	if !exists {
		return ""
	}
	if mc, ok := claims.(*jwt.MapClaims); ok {
		if r, ok := (*mc)["role"].(string); ok {
			return r
		}
	}
	return ""
}

func ListDashboards(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetInt64("user_id")
		isAdmin := getUserRole(c) == "admin"

		var rows *sql.Rows
		var err error
		if isAdmin {
			rows, err = db.Query(`
				SELECT d.id, d.name, d.description, d.owner_id, u.username,
					COALESCE(up.dashboard_id IS NOT NULL, FALSE),
					d.is_public, d.config, d.created_at, d.updated_at
				FROM dashboards d
				JOIN users u ON d.owner_id = u.id
				LEFT JOIN user_dashboard_pins up ON d.id = up.dashboard_id AND up.user_id = $1
				ORDER BY up.dashboard_id IS NOT NULL DESC, d.created_at DESC
			`, userID)
		} else {
			rows, err = db.Query(`
				SELECT d.id, d.name, d.description, d.owner_id, u.username,
					COALESCE(up.dashboard_id IS NOT NULL, FALSE),
					d.is_public, d.config, d.created_at, d.updated_at
				FROM dashboards d
				JOIN users u ON d.owner_id = u.id
				LEFT JOIN user_dashboard_pins up ON d.id = up.dashboard_id AND up.user_id = $1
				WHERE d.owner_id = $1 OR d.is_public = TRUE
				ORDER BY up.dashboard_id IS NOT NULL DESC, d.created_at DESC
			`, userID)
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		defer rows.Close()

		var dashboards []model.Dashboard
		for rows.Next() {
			var d model.Dashboard
			err := rows.Scan(&d.ID, &d.Name, &d.Description, &d.OwnerID,
				&d.OwnerUsername, &d.Pinned, &d.IsPublic, &d.Config, &d.CreatedAt, &d.UpdatedAt)
			if err != nil {
				continue
			}
			dashboards = append(dashboards, d)
		}

		c.JSON(http.StatusOK, dashboards)
	}
}

func CreateDashboard(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetInt64("user_id")

		var req struct {
			Name        string          `json:"name"`
			Description *string         `json:"description"`
			Config      json.RawMessage `json:"config"`
		}

		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		if req.Name == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
			return
		}

		if req.Config == nil {
			req.Config = json.RawMessage(`{"devices":[],"fields":[],"filters":{}}`)
		}

		var id int64
		err := db.QueryRow(`
			INSERT INTO dashboards (name, description, owner_id, config)
			VALUES ($1, $2, $3, $4) RETURNING id
		`, req.Name, req.Description, userID, req.Config).Scan(&id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusCreated, gin.H{"id": id, "message": "dashboard created"})
	}
}

func GetDashboard(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
			return
		}

		userID := c.GetInt64("user_id")

		var d model.Dashboard
		var pinned sql.NullBool
		err = db.QueryRow(`
			SELECT d.id, d.name, d.description, d.owner_id, u.username,
				up.dashboard_id IS NOT NULL,
				d.is_public, d.config, d.created_at, d.updated_at
			FROM dashboards d
			JOIN users u ON d.owner_id = u.id
			LEFT JOIN user_dashboard_pins up ON d.id = up.dashboard_id AND up.user_id = $2
			WHERE d.id = $1 AND (d.owner_id = $2 OR d.is_public = TRUE)
		`, id, userID).Scan(&d.ID, &d.Name, &d.Description, &d.OwnerID,
			&d.OwnerUsername, &pinned, &d.IsPublic, &d.Config, &d.CreatedAt, &d.UpdatedAt)
		d.Pinned = pinned.Valid && pinned.Bool
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "dashboard not found"})
			return
		}

		c.JSON(http.StatusOK, d)
	}
}

func UpdateDashboard(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
			return
		}

		userID := c.GetInt64("user_id")
		isAdmin := getUserRole(c) == "admin"

		var req struct {
			Name        *string         `json:"name"`
			Description *string         `json:"description"`
			Config      json.RawMessage `json:"config"`
		}

		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		exists := false
		if isAdmin {
			db.QueryRow("SELECT EXISTS(SELECT 1 FROM dashboards WHERE id = $1)", id).Scan(&exists)
		} else {
			db.QueryRow("SELECT EXISTS(SELECT 1 FROM dashboards WHERE id = $1 AND owner_id = $2)", id, userID).Scan(&exists)
		}
		if !exists {
			c.JSON(http.StatusNotFound, gin.H{"error": "dashboard not found"})
			return
		}

		var setClauses []string
		var args []interface{}
		argIdx := 1

		if req.Name != nil {
			setClauses = append(setClauses, "name = $"+strconv.Itoa(argIdx))
			args = append(args, *req.Name)
			argIdx++
		}
		if req.Description != nil {
			setClauses = append(setClauses, "description = $"+strconv.Itoa(argIdx))
			args = append(args, *req.Description)
			argIdx++
		}
		if req.Config != nil {
			setClauses = append(setClauses, "config = $"+strconv.Itoa(argIdx))
			args = append(args, req.Config)
			argIdx++
		}

		if len(setClauses) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no fields to update"})
			return
		}

		setClauses = append(setClauses, "updated_at = NOW()")
		args = append(args, id)

		query := "UPDATE dashboards SET " + joinStrings(setClauses, ", ") + " WHERE id = $" + strconv.Itoa(argIdx)

		_, err = db.Exec(query, args...)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "dashboard updated"})
	}
}

func DeleteDashboard(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
			return
		}

		userID := c.GetInt64("user_id")
		isAdmin := getUserRole(c) == "admin"

		var result sql.Result
		if isAdmin {
			result, err = db.Exec("DELETE FROM dashboards WHERE id = $1", id)
		} else {
			result, err = db.Exec("DELETE FROM dashboards WHERE id = $1 AND owner_id = $2", id, userID)
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		n, _ := result.RowsAffected()
		if n == 0 {
			c.JSON(http.StatusNotFound, gin.H{"error": "dashboard not found"})
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "dashboard deleted"})
	}
}

func GetDashboardData(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
			return
		}

		userID := c.GetInt64("user_id")

		var configRaw json.RawMessage
		err = db.QueryRow("SELECT config FROM dashboards WHERE id = $1 AND (owner_id = $2 OR is_public = TRUE)", id, userID).
			Scan(&configRaw)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "dashboard not found"})
			return
		}

		var cfg model.DashboardConfig
		if err := json.Unmarshal(configRaw, &cfg); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid dashboard config"})
			return
		}

		limit := c.DefaultQuery("limit", "100")
		offset := c.DefaultQuery("offset", "0")
		limitInt, _ := strconv.Atoi(limit)
		offsetInt, _ := strconv.Atoi(offset)

		if limitInt <= 0 || limitInt > 500 {
			limitInt = 100
		}

		query := "SELECT id, timestamp, hostname, app_name, process_id, msg_id, severity, facility, message, raw_message, parsed_fields, matched_parsers, created_at FROM syslog_logs WHERE 1=1"
		args := []interface{}{}
		argIdx := 1

		if len(cfg.Devices) > 0 {
			placeholders := make([]string, len(cfg.Devices))
			for i, d := range cfg.Devices {
				placeholders[i] = "$" + strconv.Itoa(argIdx)
				args = append(args, d)
				argIdx++
			}
			query += " AND hostname IN (" + strings.Join(placeholders, ", ") + ")"
		}

		severityFilter := c.DefaultQuery("severity", "")
		if severityFilter == "" {
			severityFilter = cfg.Filters.Severity
		}
		if severityFilter != "" {
			query += " AND severity = $" + strconv.Itoa(argIdx)
			args = append(args, severityFilter)
			argIdx++
		}

		fromFilter := c.DefaultQuery("from", "")
		if fromFilter == "" {
			fromFilter = cfg.Filters.From
		}
		if fromFilter != "" {
			query += " AND timestamp >= $" + strconv.Itoa(argIdx)
			args = append(args, fromFilter)
			argIdx++
		}

		toFilter := c.DefaultQuery("to", "")
		if toFilter == "" {
			toFilter = cfg.Filters.To
		}
		if toFilter != "" {
			query += " AND timestamp <= $" + strconv.Itoa(argIdx)
			args = append(args, toFilter)
			argIdx++
		}

		searchTerm := c.DefaultQuery("search", "")
		if searchTerm == "" {
			searchTerm = cfg.Filters.Search
		}
		if searchTerm != "" {
			query += " AND (message ILIKE $" + strconv.Itoa(argIdx) + " OR hostname ILIKE $" + strconv.Itoa(argIdx+1) + ")"
			searchPattern := "%" + searchTerm + "%"
			args = append(args, searchPattern, searchPattern)
			argIdx += 2
		}

		query += " ORDER BY timestamp DESC LIMIT $" + strconv.Itoa(argIdx)
		args = append(args, limitInt)
		argIdx++

		query += " OFFSET $" + strconv.Itoa(argIdx)
		args = append(args, offsetInt)

		rows, err := db.Query(query, args...)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		defer rows.Close()

		var logs []model.SyslogLog
		for rows.Next() {
			var l model.SyslogLog
			var rawParsed json.RawMessage
			var parsers pq.StringArray
			err := rows.Scan(&l.ID, &l.Timestamp, &l.Hostname, &l.AppName,
				&l.ProcessID, &l.MsgID, &l.Severity, &l.Facility,
				&l.Message, &l.RawMessage, &rawParsed, &parsers, &l.CreatedAt)
			if err != nil {
				continue
			}
			l.MatchedParsers = parsers

			if len(rawParsed) > 0 {
				json.Unmarshal(rawParsed, &l.ParsedFields)
			}

			logs = append(logs, l)
		}

		countQuery := "SELECT COUNT(*) FROM syslog_logs WHERE 1=1"
		countArgs := []interface{}{}
		countIdx := 1

		if len(cfg.Devices) > 0 {
			placeholders := make([]string, len(cfg.Devices))
			for i, d := range cfg.Devices {
				placeholders[i] = "$" + strconv.Itoa(countIdx)
				countArgs = append(countArgs, d)
				countIdx++
			}
			countQuery += " AND hostname IN (" + strings.Join(placeholders, ", ") + ")"
		}

		if cfg.Filters.Severity != "" {
			countQuery += " AND severity = $" + strconv.Itoa(countIdx)
			countArgs = append(countArgs, cfg.Filters.Severity)
			countIdx++
		}

		if cfg.Filters.From != "" {
			countQuery += " AND timestamp >= $" + strconv.Itoa(countIdx)
			countArgs = append(countArgs, cfg.Filters.From)
			countIdx++
		}

		if cfg.Filters.To != "" {
			countQuery += " AND timestamp <= $" + strconv.Itoa(countIdx)
			countArgs = append(countArgs, cfg.Filters.To)
			countIdx++
		}

		if cfg.Filters.Search != "" {
			countQuery += " AND (message ILIKE $" + strconv.Itoa(countIdx) + " OR hostname ILIKE $" + strconv.Itoa(countIdx+1) + ")"
			searchPattern := "%" + cfg.Filters.Search + "%"
			countArgs = append(countArgs, searchPattern, searchPattern)
		}

		var total int64
		db.QueryRow(countQuery, countArgs...).Scan(&total)

		resp := model.DashboardDataResponse{
			Logs:    logs,
			Total:   total,
			Fields:  cfg.Fields,
			Devices: cfg.Devices,
		}

		c.JSON(http.StatusOK, resp)
	}
}

func TogglePinDashboard(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
			return
		}

		userID := c.GetInt64("user_id")

		exists := false
		err = db.QueryRow("SELECT EXISTS(SELECT 1 FROM dashboards WHERE id = $1 AND (owner_id = $2 OR is_public = TRUE))", id, userID).Scan(&exists)
		if err != nil || !exists {
			c.JSON(http.StatusNotFound, gin.H{"error": "dashboard not found"})
			return
		}

		var pinned bool
		err = db.QueryRow("SELECT EXISTS(SELECT 1 FROM user_dashboard_pins WHERE user_id = $1 AND dashboard_id = $2)", userID, id).Scan(&pinned)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		if pinned {
			db.Exec("DELETE FROM user_dashboard_pins WHERE user_id = $1 AND dashboard_id = $2", userID, id)
		} else {
			db.Exec("INSERT INTO user_dashboard_pins (user_id, dashboard_id) VALUES ($1, $2) ON CONFLICT DO NOTHING", userID, id)
		}

		c.JSON(http.StatusOK, gin.H{"pinned": !pinned})
	}
}

func TogglePublicDashboard(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
			return
		}

		userID := c.GetInt64("user_id")
		isAdmin := getUserRole(c) == "admin"

		var isPublic bool
		if isAdmin {
			err = db.QueryRow("SELECT is_public FROM dashboards WHERE id = $1", id).Scan(&isPublic)
		} else {
			err = db.QueryRow("SELECT is_public FROM dashboards WHERE id = $1 AND owner_id = $2", id, userID).Scan(&isPublic)
		}
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "dashboard not found"})
			return
		}

		newPublic := !isPublic
		if isAdmin {
			_, err = db.Exec("UPDATE dashboards SET is_public = $1, updated_at = NOW() WHERE id = $2", newPublic, id)
		} else {
			_, err = db.Exec("UPDATE dashboards SET is_public = $1, updated_at = NOW() WHERE id = $2 AND owner_id = $3", newPublic, id, userID)
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"is_public": newPublic})
	}
}


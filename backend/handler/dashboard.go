package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"syslog-gui/middleware"
	"syslog-gui/model"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
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
		isAdmin := getUserRole(c) == RoleAdmin

		var rows *sql.Rows
		var err error
		if isAdmin {
			rows, err = db.Query(`
				SELECT d.id, d.name, d.description, d.owner_id, u.username,
					COALESCE(up.dashboard_id IS NOT NULL, FALSE),
					d.is_public, d.config, d.created_at, d.updated_at,
					d.updated_by, ub.username
				FROM dashboards d
				JOIN users u ON d.owner_id = u.id
				LEFT JOIN user_dashboard_pins up ON d.id = up.dashboard_id AND up.user_id = $1
				LEFT JOIN users ub ON d.updated_by = ub.id
				ORDER BY up.dashboard_id IS NOT NULL DESC, d.created_at DESC
			`, userID)
		} else {
			rows, err = db.Query(`
				SELECT d.id, d.name, d.description, d.owner_id, u.username,
					COALESCE(up.dashboard_id IS NOT NULL, FALSE),
					d.is_public, d.config, d.created_at, d.updated_at,
					d.updated_by, ub.username
				FROM dashboards d
				JOIN users u ON d.owner_id = u.id
				LEFT JOIN user_dashboard_pins up ON d.id = up.dashboard_id AND up.user_id = $1
				LEFT JOIN users ub ON d.updated_by = ub.id
				WHERE d.owner_id = $1 OR d.is_public = TRUE
				ORDER BY up.dashboard_id IS NOT NULL DESC, d.created_at DESC
			`, userID)
		}
		if err != nil {
			middleware.HandleError(c, model.NewInternal("Query failed", err))
			return
		}
		defer rows.Close()

		var dashboards []model.Dashboard
		for rows.Next() {
			var d model.Dashboard
			var updatedBy sql.NullInt64
			var updatedByUsername sql.NullString
			err := rows.Scan(&d.ID, &d.Name, &d.Description, &d.OwnerID,
				&d.OwnerUsername, &d.Pinned, &d.IsPublic, &d.Config, &d.CreatedAt, &d.UpdatedAt,
				&updatedBy, &updatedByUsername)
			if err != nil {
				continue
			}
			if updatedBy.Valid {
				d.UpdatedByUserID = updatedBy.Int64
			}
			if updatedByUsername.Valid {
				d.UpdatedByUsername = updatedByUsername.String
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
			middleware.HandleError(c, model.NewBadRequest("Invalid request body", err))
			return
		}

		if req.Name == "" {
			middleware.HandleError(c, model.NewBadRequest("name is required", nil))
			return
		}

		if req.Config == nil {
			req.Config = json.RawMessage(`{"devices":[],"fields":[],"filters":{}}`)
		}

		var id int64
		err := db.QueryRow(`
			INSERT INTO dashboards (name, description, owner_id, config, updated_by)
			VALUES ($1, $2, $3, $4, $5) RETURNING id
		`, req.Name, req.Description, userID, req.Config, userID).Scan(&id)
		if err != nil {
			middleware.HandleError(c, model.NewInternal("Failed to create dashboard", err))
			return
		}

		c.JSON(http.StatusCreated, gin.H{"id": id, "message": "dashboard created"})
	}
}

func GetDashboard(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := parseIDParam(c.Param("id"))
		if err != nil {
			middleware.HandleError(c, model.NewBadRequest("invalid id", nil))
			return
		}

		userID := c.GetInt64("user_id")
		isAdmin := getUserRole(c) == RoleAdmin

		var d model.Dashboard
		var pinned sql.NullBool
		var updatedBy sql.NullInt64
		var updatedByUsername sql.NullString
		whereClause := "d.id = $1 AND (d.owner_id = $2 OR d.is_public = TRUE)"
		if isAdmin {
			whereClause = "d.id = $1"
		}
		err = db.QueryRow(fmt.Sprintf(`
			SELECT d.id, d.name, d.description, d.owner_id, u.username,
				up.dashboard_id IS NOT NULL,
				d.is_public, d.config, d.created_at, d.updated_at,
				d.updated_by, ub.username
			FROM dashboards d
			JOIN users u ON d.owner_id = u.id
			LEFT JOIN user_dashboard_pins up ON d.id = up.dashboard_id AND up.user_id = $2
			LEFT JOIN users ub ON d.updated_by = ub.id
			WHERE %s
		`, whereClause), id, userID).Scan(&d.ID, &d.Name, &d.Description, &d.OwnerID,
			&d.OwnerUsername, &pinned, &d.IsPublic, &d.Config, &d.CreatedAt, &d.UpdatedAt,
			&updatedBy, &updatedByUsername)
		d.Pinned = pinned.Valid && pinned.Bool
		if updatedBy.Valid {
			d.UpdatedByUserID = updatedBy.Int64
		}
		if updatedByUsername.Valid {
			d.UpdatedByUsername = updatedByUsername.String
		}
		if err != nil {
			middleware.HandleError(c, model.NewNotFound("dashboard not found", err))
			return
		}

		c.JSON(http.StatusOK, d)
	}
}

func UpdateDashboard(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := parseIDParam(c.Param("id"))
		if err != nil {
			middleware.HandleError(c, model.NewBadRequest("invalid id", nil))
			return
		}

		userID := c.GetInt64("user_id")
		isAdmin := getUserRole(c) == RoleAdmin

		var req struct {
			Name        *string         `json:"name"`
			Description *string         `json:"description"`
			Config      json.RawMessage `json:"config"`
		}

		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.HandleError(c, model.NewBadRequest("Invalid request body", err))
			return
		}

		exists := false
		if isAdmin {
			db.QueryRow("SELECT EXISTS(SELECT 1 FROM dashboards WHERE id = $1)", id).Scan(&exists)
		} else {
			db.QueryRow("SELECT EXISTS(SELECT 1 FROM dashboards WHERE id = $1 AND owner_id = $2)", id, userID).Scan(&exists)
		}
		if !exists {
			middleware.HandleError(c, model.NewNotFound("dashboard not found", nil))
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
			middleware.HandleError(c, model.NewBadRequest("no fields to update", nil))
			return
		}

		setClauses = append(setClauses, "updated_at = NOW()")
		setClauses = append(setClauses, "updated_by = $"+strconv.Itoa(argIdx))
		args = append(args, userID)
		argIdx++
		args = append(args, id)

		query := "UPDATE dashboards SET " + joinStrings(setClauses, ", ") + " WHERE id = $" + strconv.Itoa(argIdx)

		_, err = db.Exec(query, args...)
		if err != nil {
			middleware.HandleError(c, model.NewInternal("Failed to update dashboard", err))
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "dashboard updated"})
	}
}

func DeleteDashboard(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := parseIDParam(c.Param("id"))
		if err != nil {
			middleware.HandleError(c, model.NewBadRequest("invalid id", nil))
			return
		}

		userID := c.GetInt64("user_id")
		isAdmin := getUserRole(c) == RoleAdmin

		var result sql.Result
		if isAdmin {
			result, err = db.Exec("DELETE FROM dashboards WHERE id = $1", id)
		} else {
			result, err = db.Exec("DELETE FROM dashboards WHERE id = $1 AND owner_id = $2", id, userID)
		}
		if err != nil {
			middleware.HandleError(c, model.NewInternal("Failed to delete dashboard", err))
			return
		}

		n, _ := result.RowsAffected()
		if n == 0 {
			middleware.HandleError(c, model.NewNotFound("dashboard not found", nil))
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "dashboard deleted"})
	}
}

func GetDashboardData(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := parseIDParam(c.Param("id"))
		if err != nil {
			middleware.HandleError(c, model.NewBadRequest("invalid id", nil))
			return
		}

		userID := c.GetInt64("user_id")
		isAdmin := getUserRole(c) == RoleAdmin

		configRaw, err := getDashboardConfig(db, id, userID, isAdmin)
		if err != nil {
			middleware.HandleError(c, model.NewNotFound("dashboard not found", err))
			return
		}

		cfg, err := parseDashboardConfig(configRaw)
		if err != nil {
			middleware.HandleError(c, model.NewBadRequest("invalid dashboard config", err))
			return
		}

		limitInt, offsetInt := parsePagination(c, DefaultPageLimit, MaxPageLimit)

		severityFilter := firstNonEmpty(c.DefaultQuery("severity", ""), cfg.Filters.Severity)
		fromFilter := firstNonEmpty(c.DefaultQuery("from", ""), cfg.Filters.From)
		toFilter := firstNonEmpty(c.DefaultQuery("to", ""), cfg.Filters.To)
		searchTerm := firstNonEmpty(c.DefaultQuery("search", ""), cfg.Filters.Search)

		opts := LogFilterOptions{
			Severity:  severityFilter,
			From:      fromFilter,
			To:        toFilter,
			Devices:   cfg.Devices,
			HasFields: len(cfg.Fields) > 0,
		}
		whereClauses, args, argIdx := buildLogWhereClauses(opts)

		if searchTerm != "" {
			whereClauses = append(whereClauses, fmt.Sprintf("message ILIKE $%d OR hostname ILIKE $%d", argIdx, argIdx+1))
			pattern := "%" + searchTerm + "%"
			args = append(args, pattern, pattern)
			argIdx += 2
		}

		whereSQL := buildWhereSQL(whereClauses)

		logsQuery := fmt.Sprintf(
			"SELECT id, timestamp, hostname, fromhost_ip, app_name, process_id, msg_id, severity, facility, message, raw_message, parsed_fields, matched_parsers, created_at, '' "+
				"FROM syslog_logs %s ORDER BY timestamp DESC LIMIT $%d OFFSET $%d",
			whereSQL, argIdx, argIdx+1,
		)
		args = append(args, limitInt, offsetInt)

		var logs []model.SyslogLog
		_ = timedQuery("dashboard_data_logs", func() error {
			rows, err := db.Query(logsQuery, args...)
			if err != nil {
				return err
			}
			defer rows.Close()

			logs = scanLogRows(rows)
			return nil
		})

		countOpts := LogFilterOptions{
			Severity:  cfg.Filters.Severity,
			From:      cfg.Filters.From,
			To:        cfg.Filters.To,
			Devices:   cfg.Devices,
			HasFields: len(cfg.Fields) > 0,
		}
		countClauses, countArgs, _ := buildLogWhereClauses(countOpts)

		if cfg.Filters.Search != "" {
			countClauses = append(countClauses, fmt.Sprintf("message ILIKE $%d OR hostname ILIKE $%d", len(countArgs)+1, len(countArgs)+2))
			pattern := "%" + cfg.Filters.Search + "%"
			countArgs = append(countArgs, pattern, pattern)
		}

		countSQL := buildWhereSQL(countClauses)
		var total int64
		_ = timedQuery("dashboard_data_count", func() error {
			return db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM syslog_logs %s", countSQL), countArgs...).Scan(&total)
		})

		c.JSON(http.StatusOK, model.DashboardDataResponse{
			Logs:    logs,
			Total:   total,
			Fields:  cfg.Fields,
			Devices: cfg.Devices,
		})
	}
}

func TogglePinDashboard(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := parseIDParam(c.Param("id"))
		if err != nil {
			middleware.HandleError(c, model.NewBadRequest("invalid id", nil))
			return
		}

		userID := c.GetInt64("user_id")
		isAdmin := getUserRole(c) == RoleAdmin

		exists := false
		if isAdmin {
			err = db.QueryRow("SELECT EXISTS(SELECT 1 FROM dashboards WHERE id = $1)", id).Scan(&exists)
		} else {
			err = db.QueryRow("SELECT EXISTS(SELECT 1 FROM dashboards WHERE id = $1 AND (owner_id = $2 OR is_public = TRUE))", id, userID).Scan(&exists)
		}
		if err != nil || !exists {
			middleware.HandleError(c, model.NewNotFound("dashboard not found", err))
			return
		}

		var pinned bool
		err = db.QueryRow("SELECT EXISTS(SELECT 1 FROM user_dashboard_pins WHERE user_id = $1 AND dashboard_id = $2)", userID, id).Scan(&pinned)
		if err != nil {
			middleware.HandleError(c, model.NewInternal("Query failed", err))
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
		id, err := parseIDParam(c.Param("id"))
		if err != nil {
			middleware.HandleError(c, model.NewBadRequest("invalid id", nil))
			return
		}

		userID := c.GetInt64("user_id")
		isAdmin := getUserRole(c) == RoleAdmin

		var isPublic bool
		if isAdmin {
			err = db.QueryRow("SELECT is_public FROM dashboards WHERE id = $1", id).Scan(&isPublic)
		} else {
			err = db.QueryRow("SELECT is_public FROM dashboards WHERE id = $1 AND owner_id = $2", id, userID).Scan(&isPublic)
		}
		if err != nil {
			middleware.HandleError(c, model.NewNotFound("dashboard not found", err))
			return
		}

		newPublic := !isPublic
		if isAdmin {
			_, err = db.Exec("UPDATE dashboards SET is_public = $1, updated_at = NOW() WHERE id = $2", newPublic, id)
		} else {
			_, err = db.Exec("UPDATE dashboards SET is_public = $1, updated_at = NOW() WHERE id = $2 AND owner_id = $3", newPublic, id, userID)
		}
		if err != nil {
			middleware.HandleError(c, model.NewInternal("Failed to update dashboard", err))
			return
		}

		c.JSON(http.StatusOK, gin.H{"is_public": newPublic})
	}
}

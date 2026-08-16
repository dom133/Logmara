package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"logmara/db"
	"logmara/middleware"
	"logmara/model"

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

func ListDashboards(pool *db.DynamicPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		database := pool.Get()
		userID := c.GetInt64("user_id")
		isAdmin := getUserRole(c) == RoleAdmin

		var rows *sql.Rows
		var err error
		if isAdmin {
			rows, err = database.Query(`
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
			rows, err = database.Query(`
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

func CreateDashboard(pool *db.DynamicPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		database := pool.Get()
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
			middleware.HandleError(c, model.NewBadRequestKey("dashboard.nameRequired", "Dashboard name is required", nil))
			return
		}

		if req.Config == nil {
			req.Config = json.RawMessage(`{"devices":[],"fields":[],"filters":{}}`)
		}

		var id int64
		err := database.QueryRow(`
			INSERT INTO dashboards (name, description, owner_id, config, updated_by)
			VALUES ($1, $2, $3, $4, $5) RETURNING id
		`, req.Name, req.Description, userID, req.Config, userID).Scan(&id)
		if err != nil {
			middleware.HandleError(c, model.NewInternalKey("dashboard.createFailed", "Failed to create dashboard", err))
			return
		}

		c.JSON(http.StatusCreated, gin.H{"id": id, "message": "dashboard created"})
	}
}

func GetDashboard(pool *db.DynamicPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		database := pool.Get()
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
		err = database.QueryRow(fmt.Sprintf(`
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

func UpdateDashboard(pool *db.DynamicPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		database := pool.Get()
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
			database.QueryRow("SELECT EXISTS(SELECT 1 FROM dashboards WHERE id = $1)", id).Scan(&exists)
		} else {
			database.QueryRow("SELECT EXISTS(SELECT 1 FROM dashboards WHERE id = $1 AND owner_id = $2)", id, userID).Scan(&exists)
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
			middleware.HandleError(c, model.NewBadRequestKey("dashboard.noFieldsToUpdate", "No fields to update", nil))
			return
		}

		setClauses = append(setClauses, "updated_at = NOW()")
		setClauses = append(setClauses, "updated_by = $"+strconv.Itoa(argIdx))
		args = append(args, userID)
		argIdx++
		args = append(args, id)

		query := "UPDATE dashboards SET " + joinStrings(setClauses, ", ") + " WHERE id = $" + strconv.Itoa(argIdx)

		_, err = database.Exec(query, args...)
		if err != nil {
			middleware.HandleError(c, model.NewInternal("Failed to update dashboard", err))
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "dashboard updated"})
	}
}

func DeleteDashboard(pool *db.DynamicPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		database := pool.Get()
		id, err := parseIDParam(c.Param("id"))
		if err != nil {
			middleware.HandleError(c, model.NewBadRequest("invalid id", nil))
			return
		}

		userID := c.GetInt64("user_id")
		isAdmin := getUserRole(c) == RoleAdmin

		var result sql.Result
		if isAdmin {
			result, err = database.Exec("DELETE FROM dashboards WHERE id = $1", id)
		} else {
			result, err = database.Exec("DELETE FROM dashboards WHERE id = $1 AND owner_id = $2", id, userID)
		}
		if err != nil {
			middleware.HandleError(c, model.NewInternalKey("dashboard.deleteFailed", "Failed to delete dashboard", err))
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

type DashboardFilterRequest struct {
	Severity     string `json:"severity"`
	From         string `json:"from"`
	To           string `json:"to"`
	Search       string `json:"search"`
	FromHostIP   string `json:"fromhost_ip"`
	FieldFilters string `json:"field_filters"`
}

// resolveDashboardFilters loads a dashboard's config and merges it with
// the POST body overrides, returning filter options ready for
// buildLogWhereClauses.
func resolveDashboardFilters(pool *db.DynamicPool, c *gin.Context, req DashboardFilterRequest) (*model.DashboardConfig, LogFilterOptions, error) {

	id, err := parseIDParam(c.Param("id"))
	if err != nil {
		return nil, LogFilterOptions{}, model.NewBadRequest("invalid id", nil)
	}

	userID := c.GetInt64("user_id")
	isAdmin := getUserRole(c) == RoleAdmin

	configRaw, err := getDashboardConfig(pool, id, userID, isAdmin)
	if err != nil {
		return nil, LogFilterOptions{}, model.NewNotFound("dashboard not found", err)
	}

	cfg, err := parseDashboardConfig(configRaw)
	if err != nil {
		return nil, LogFilterOptions{}, model.NewBadRequest("invalid dashboard config", err)
	}

	requiredParsers, err := resolveParsersForFields(pool, cfg.Fields)
	if err != nil {
		return nil, LogFilterOptions{}, model.NewInternal("failed to resolve parsers for fields", err)
	}

	opts := LogFilterOptions{
		Severity:        firstNonEmpty(req.Severity, cfg.Filters.Severity),
		From:            firstNonEmpty(req.From, cfg.Filters.From),
		To:              firstNonEmpty(req.To, cfg.Filters.To),
		Search:          firstNonEmpty(req.Search, cfg.Filters.Search),
		Devices:         cfg.Devices,
		RequiredParsers: requiredParsers,
		FieldFilters:    cfg.Filters.FieldFilters,
	}

	if ffStr := req.FieldFilters; ffStr != "" {
		var ff []model.FieldFilter
		if err := json.Unmarshal([]byte(ffStr), &ff); err == nil && len(ff) > 0 {
			opts.FieldFilters = ff
		}
	}

	if fromHostIP := req.FromHostIP; fromHostIP != "" {
		if len(cfg.Devices) == 0 || containsString(cfg.Devices, fromHostIP) {
			opts.Devices = []string{fromHostIP}
		}
	}

	return cfg, opts, nil
}

// resolveDashboardFiltersWithName is like resolveDashboardFilters but also
// returns the dashboard's name for use in export filenames.
func resolveDashboardFiltersWithName(pool *db.DynamicPool, c *gin.Context, req DashboardFilterRequest) (*model.DashboardConfig, LogFilterOptions, string, error) {
	database := pool.Get()
	id, err := parseIDParam(c.Param("id"))
	if err != nil {
		return nil, LogFilterOptions{}, "", model.NewBadRequest("invalid id", nil)
	}

	userID := c.GetInt64("user_id")
	isAdmin := getUserRole(c) == RoleAdmin

	var dashName string
	var configRaw json.RawMessage
	if isAdmin {
		err = database.QueryRow("SELECT name, config FROM dashboards WHERE id = $1", id).Scan(&dashName, &configRaw)
	} else {
		err = database.QueryRow("SELECT name, config FROM dashboards WHERE id = $1 AND (owner_id = $2 OR is_public = TRUE)", id, userID).Scan(&dashName, &configRaw)
	}
	if err != nil {
		return nil, LogFilterOptions{}, "", model.NewNotFound("dashboard not found", err)
	}

	cfg, err := parseDashboardConfig(configRaw)
	if err != nil {
		return nil, LogFilterOptions{}, "", model.NewBadRequest("invalid dashboard config", err)
	}

	requiredParsers, err := resolveParsersForFields(pool, cfg.Fields)
	if err != nil {
		return nil, LogFilterOptions{}, "", model.NewInternal("failed to resolve parsers for fields", err)
	}

	opts := LogFilterOptions{
		Severity:        firstNonEmpty(req.Severity, cfg.Filters.Severity),
		From:            firstNonEmpty(req.From, cfg.Filters.From),
		To:              firstNonEmpty(req.To, cfg.Filters.To),
		Search:          firstNonEmpty(req.Search, cfg.Filters.Search),
		Devices:         cfg.Devices,
		RequiredParsers: requiredParsers,
		FieldFilters:    cfg.Filters.FieldFilters,
	}

	if ffStr := req.FieldFilters; ffStr != "" {
		var ff []model.FieldFilter
		if err := json.Unmarshal([]byte(ffStr), &ff); err == nil && len(ff) > 0 {
			opts.FieldFilters = ff
		}
	}

	if fromHostIP := req.FromHostIP; fromHostIP != "" {
		if len(cfg.Devices) == 0 || containsString(cfg.Devices, fromHostIP) {
			opts.Devices = []string{fromHostIP}
		}
	}

	return cfg, opts, dashName, nil
}

func containsString(list []string, target string) bool {
	for _, v := range list {
		if v == target {
			return true
		}
	}
	return false
}

// resolveParsersForFields maps a dashboard's selected field names back to
// the parser(s) that own them, so log rows can be checked against
// matched_parsers (verifying they were actually parsed by that parser)
// rather than just showing up because they matched some unrelated parser.
func resolveParsersForFields(pool *db.DynamicPool, fields []string) ([]string, error) {
	database := pool.Get()
	if len(fields) == 0 {
		return nil, nil
	}

	rows, err := database.Query(`
		SELECT DISTINCT p.name
		FROM parsed_fields_registry f
		JOIN parsers p ON f.parser_id = p.id
		WHERE f.field_name = ANY($1)
	`, pq.Array(fields))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			continue
		}
		names = append(names, name)
	}
	return names, nil
}

type DashboardDataRequest struct {
	Severity     string `json:"severity"`
	From         string `json:"from"`
	To           string `json:"to"`
	Search       string `json:"search"`
	FromHostIP   string `json:"fromhost_ip"`
	FieldFilters string `json:"field_filters"`
	Limit        string `json:"limit"`
	Offset       string `json:"offset"`
	Cursor       string `json:"cursor"`
	Sort         string `json:"sort"`
}

func GetDashboardData(pool *db.DynamicPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		// filteredQueryTimeout (20s) can exceed the server's default
		// WriteTimeout (15s, see main.go) on dashboards with many
		// fields/large field filters, since matching on parsed fields can be
		// slow. Without this, the server hard-closes the connection before
		// the query context even has a chance to cancel or return, and the
		// client sees a raw 502 from the reverse proxy instead of a clean
		// JSON timeout error.
		http.NewResponseController(c.Writer).SetWriteDeadline(time.Now().Add(filteredQueryTimeout + 5*time.Second))
		database := pool.Get()
		var req DashboardDataRequest
		_ = c.ShouldBindJSON(&req)

		filterReq := DashboardFilterRequest{
			Severity:     req.Severity,
			From:         req.From,
			To:           req.To,
			Search:       req.Search,
			FromHostIP:   req.FromHostIP,
			FieldFilters: req.FieldFilters,
		}
		cfg, opts, err := resolveDashboardFilters(pool, c, filterReq)
		if err != nil {
			middleware.HandleError(c, err)
			return
		}

		limitStr := req.Limit
		offsetStr := req.Offset
		limitInt, offsetInt := parsePaginationFromStrings(limitStr, offsetStr, DefaultPageLimit, MaxPageLimit)
		cursor := req.Cursor
		sort := req.Sort
		if sort == "" {
			sort = "timestamp_desc"
		}

		whereClauses, args, argIdx := buildLogWhereClauses(opts)

		orderClause := "timestamp DESC, id DESC"
		cursorOp := "<"
		switch sort {
		case "timestamp_asc":
			orderClause = "timestamp ASC, id ASC"
			cursorOp = ">"
		case "severity":
			orderClause = "severity ASC, timestamp DESC, id DESC"
		case "hostname":
			orderClause = "hostname ASC, timestamp DESC, id DESC"
		}
		useCursor := cursorSupported(sort)

		if useCursor && cursor != "" {
			ts, id, err := decodeLogCursor(cursor)
			if err != nil {
				middleware.HandleError(c, model.NewBadRequestKey("error.invalidCursor", "Invalid cursor", err))
				return
			}
			whereClauses = append(whereClauses, fmt.Sprintf("(timestamp, id) %s ($%d, $%d)", cursorOp, argIdx, argIdx+1))
			args = append(args, ts, id)
			argIdx += 2
			offsetInt = 0
		}

		whereSQL := buildWhereSQL(whereClauses)

		// Fetch one extra row to detect more pages instead of a separate
		// exact COUNT(*)/materialized-view lookup on every request. Sorts
		// other than timestamp fall back to OFFSET paging, same tradeoff as
		// GetLogs (see cursorSupported).
		var logsQuery string
		if useCursor {
			logsQuery = fmt.Sprintf(
				"SELECT id, timestamp, hostname, fromhost_ip, app_name, process_id, msg_id, severity, facility, message, raw_message, parsed_fields, matched_parsers, created_at, '' "+
					"FROM syslog_logs %s ORDER BY %s LIMIT $%d",
				whereSQL, orderClause, argIdx,
			)
			args = append(args, limitInt+1)
		} else {
			logsQuery = fmt.Sprintf(
				"SELECT id, timestamp, hostname, fromhost_ip, app_name, process_id, msg_id, severity, facility, message, raw_message, parsed_fields, matched_parsers, created_at, '' "+
					"FROM syslog_logs %s ORDER BY %s LIMIT $%d OFFSET $%d",
				whereSQL, orderClause, argIdx, argIdx+1,
			)
			args = append(args, limitInt+1, offsetInt)
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), filteredQueryTimeout)
		defer cancel()

		var logs []model.SyslogLog
		_ = timedQuery("dashboard_data_logs", func() error {
			rows, err := database.QueryContext(ctx, logsQuery, args...)
			if err != nil {
				return err
			}
			defer rows.Close()

			logs = scanLogRows(rows)
			return nil
		})

		hasMore := len(logs) > limitInt
		if hasMore {
			logs = logs[:limitInt]
		}
		nextCursor := ""
		if hasMore && useCursor && len(logs) > 0 {
			last := logs[len(logs)-1]
			nextCursor = encodeLogCursor(last.Timestamp, last.ID)
		}

		c.JSON(http.StatusOK, model.DashboardDataResponse{
			Logs:       logs,
			HasMore:    hasMore,
			NextCursor: nextCursor,
			Fields:     cfg.Fields,
			Devices:    cfg.Devices,
		})
	}
}

// GetDashboardDataCount returns the exact number of rows matching the same
// filters as GetDashboardData, without paginating - a single COUNT(*) per
// filter change instead of one per page (see GetLogsCount).
func GetDashboardDataCount(pool *db.DynamicPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		// See GetDashboardData: extend the write deadline past
		// filteredQueryTimeout so a slow COUNT query times out cleanly
		// instead of the server closing the connection first.
		http.NewResponseController(c.Writer).SetWriteDeadline(time.Now().Add(filteredQueryTimeout + 5*time.Second))
		database := pool.Get()
		var req DashboardFilterRequest
		_ = c.ShouldBindJSON(&req)
		_, opts, err := resolveDashboardFilters(pool, c, req)
		if err != nil {
			middleware.HandleError(c, err)
			return
		}

		whereClauses, args, _ := buildLogWhereClauses(opts)
		whereSQL := buildWhereSQL(whereClauses)

		ctx, cancel := context.WithTimeout(c.Request.Context(), filteredQueryTimeout)
		defer cancel()

		var total int64
		if whereSQL == "" {
			// Dashboard scoped to "all devices"/no filters: an exact
			// COUNT(*) would scan every partition, so use the
			// reltuples-based estimate instead (see GetLogsCount).
			_ = timedQuery("dashboard_data_count_estimate", func() error {
				var err error
				total, err = db.EstimateSyslogLogsCount(ctx, database)
				return err
			})
		} else {
			cacheKey := "dash:" + c.Param("id") + ":" + whereSQL + fmt.Sprint(args...)
			_ = timedQuery("dashboard_data_count", func() error {
				var err error
				total, err = cachedFilteredCount(cacheKey, func() (int64, error) {
					var t int64
					err := database.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM syslog_logs %s", whereSQL), args...).Scan(&t)
					return t, err
				})
				return err
			})
		}

		c.JSON(http.StatusOK, gin.H{"total": total})
	}
}

// ExportDashboardCSV exports a dashboard's log view as CSV, honoring the
// same device/field scoping and filter overrides as GetDashboardData.
type DashboardExportRequest struct {
	Severity     string `json:"severity"`
	From         string `json:"from"`
	To           string `json:"to"`
	Search       string `json:"search"`
	FromHostIP   string `json:"fromhost_ip"`
	FieldFilters string `json:"field_filters"`
	Limit        string `json:"limit"`
}

func ExportDashboardCSV(pool *db.DynamicPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req DashboardExportRequest
		_ = c.ShouldBindJSON(&req)

		filterReq := DashboardFilterRequest{
			Severity:     req.Severity,
			From:         req.From,
			To:           req.To,
			Search:       req.Search,
			FromHostIP:   req.FromHostIP,
			FieldFilters: req.FieldFilters,
		}
		cfg, opts, dashName, err := resolveDashboardFiltersWithName(pool, c, filterReq)
		if err != nil {
			middleware.HandleError(c, err)
			return
		}

		limitStr := req.Limit
		if limitStr == "" {
			limitStr = "100000"
		}
		limit, err := strconv.Atoi(limitStr)
		if err != nil || limit < 0 {
			limit = DefaultExportLimit
		}
		if limit > MaxExportLimit && limit != 0 {
			limit = MaxExportLimit
		}

		if limit == 0 {
			http.NewResponseController(c.Writer).SetWriteDeadline(time.Time{})
		} else {
			http.NewResponseController(c.Writer).SetWriteDeadline(time.Now().Add(180 * time.Second))
		}

		whereClauses, args, _ := buildLogWhereClauses(opts)
		writeCSVExport(c, pool, buildWhereSQL(whereClauses), args, limit, cfg.Fields, dashName)
	}
}

// ExportDashboardHTML exports a dashboard's log view as an HTML report,
// honoring the same device/field scoping and filter overrides as
// GetDashboardData.
func ExportDashboardHTML(pool *db.DynamicPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		http.NewResponseController(c.Writer).SetWriteDeadline(time.Now().Add(180 * time.Second))
		var req DashboardFilterRequest
		_ = c.ShouldBindJSON(&req)
		cfg, opts, dashName, err := resolveDashboardFiltersWithName(pool, c, req)
		if err != nil {
			middleware.HandleError(c, err)
			return
		}

		whereClauses, args, _ := buildLogWhereClauses(opts)
		writeHTMLExport(c, pool, buildWhereSQL(whereClauses), args, 5000, cfg.Fields, dashName)
	}
}

func TogglePinDashboard(pool *db.DynamicPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		database := pool.Get()
		id, err := parseIDParam(c.Param("id"))
		if err != nil {
			middleware.HandleError(c, model.NewBadRequest("invalid id", nil))
			return
		}

		userID := c.GetInt64("user_id")
		isAdmin := getUserRole(c) == RoleAdmin

		exists := false
		if isAdmin {
			err = database.QueryRow("SELECT EXISTS(SELECT 1 FROM dashboards WHERE id = $1)", id).Scan(&exists)
		} else {
			err = database.QueryRow("SELECT EXISTS(SELECT 1 FROM dashboards WHERE id = $1 AND (owner_id = $2 OR is_public = TRUE))", id, userID).Scan(&exists)
		}
		if err != nil || !exists {
			middleware.HandleError(c, model.NewNotFound("dashboard not found", err))
			return
		}

		var pinned bool
		err = database.QueryRow("SELECT EXISTS(SELECT 1 FROM user_dashboard_pins WHERE user_id = $1 AND dashboard_id = $2)", userID, id).Scan(&pinned)
		if err != nil {
			middleware.HandleError(c, model.NewInternal("Query failed", err))
			return
		}

		if pinned {
			database.Exec("DELETE FROM user_dashboard_pins WHERE user_id = $1 AND dashboard_id = $2", userID, id)
		} else {
			database.Exec("INSERT INTO user_dashboard_pins (user_id, dashboard_id) VALUES ($1, $2) ON CONFLICT DO NOTHING", userID, id)
		}

		c.JSON(http.StatusOK, gin.H{"pinned": !pinned})
	}
}

func TogglePublicDashboard(pool *db.DynamicPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		database := pool.Get()
		id, err := parseIDParam(c.Param("id"))
		if err != nil {
			middleware.HandleError(c, model.NewBadRequest("invalid id", nil))
			return
		}

		userID := c.GetInt64("user_id")
		isAdmin := getUserRole(c) == RoleAdmin

		var isPublic bool
		if isAdmin {
			err = database.QueryRow("SELECT is_public FROM dashboards WHERE id = $1", id).Scan(&isPublic)
		} else {
			err = database.QueryRow("SELECT is_public FROM dashboards WHERE id = $1 AND owner_id = $2", id, userID).Scan(&isPublic)
		}
		if err != nil {
			middleware.HandleError(c, model.NewNotFound("dashboard not found", err))
			return
		}

		newPublic := !isPublic
		if isAdmin {
			_, err = database.Exec("UPDATE dashboards SET is_public = $1, updated_at = NOW() WHERE id = $2", newPublic, id)
		} else {
			_, err = database.Exec("UPDATE dashboards SET is_public = $1, updated_at = NOW() WHERE id = $2 AND owner_id = $3", newPublic, id, userID)
		}
		if err != nil {
			middleware.HandleError(c, model.NewInternal("Failed to update dashboard", err))
			return
		}

		c.JSON(http.StatusOK, gin.H{"is_public": newPublic})
	}
}

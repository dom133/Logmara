package handler

import (
	"net/http"
	"strconv"
	"strings"

	"syslog-gui/model"
	"syslog-gui/parser"

	"github.com/gin-gonic/gin"
)

func ListParsers(engine *parser.Engine) gin.HandlerFunc {
	return func(c *gin.Context) {
		parsers, err := engine.GetAllParsers()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, parsers)
	}
}

func CreateParser(engine *parser.Engine) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Name        string  `json:"name"`
			Description *string `json:"description"`
			DeviceType  string  `json:"device_type"`
			MatchType   string  `json:"match_type"`
			MatchValue  *string `json:"match_value"`
			Regex       string  `json:"regex"`
			Enabled     bool    `json:"enabled"`
			Fields      []struct {
				Name  string `json:"name"`
				Label string `json:"label"`
				Type  string `json:"type"`
			} `json:"fields"`
		}

		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		db := engine.GetDB()
		tx, err := db.Begin()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		defer tx.Rollback()

		var id int64
		err = tx.QueryRow(`
			INSERT INTO parsers (name, description, device_type, match_type, match_value, regex, enabled, is_builtin)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id
		`, req.Name, req.Description, req.DeviceType, req.MatchType, req.MatchValue, req.Regex, req.Enabled, false).
			Scan(&id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		for _, f := range req.Fields {
			ftype := f.Type
			if ftype == "" {
				ftype = "string"
			}
			_, err := tx.Exec(`
				INSERT INTO parsed_fields_registry (parser_id, field_name, field_label, field_type)
				VALUES ($1, $2, $3, $4)
			`, id, f.Name, f.Label, ftype)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
		}

		if err := tx.Commit(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		engine.Reload()
		c.JSON(http.StatusCreated, gin.H{"id": id, "message": "parser created"})
	}
}

func UpdateParser(engine *parser.Engine) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
			return
		}

		var req struct {
			Name        *string  `json:"name"`
			Description *string  `json:"description"`
			DeviceType  *string  `json:"device_type"`
			MatchType   *string  `json:"match_type"`
			MatchValue  *string  `json:"match_value"`
			Regex       *string  `json:"regex"`
			Enabled     *bool    `json:"enabled"`
		}

		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		db := engine.GetDB()

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
		if req.DeviceType != nil {
			setClauses = append(setClauses, "device_type = $"+strconv.Itoa(argIdx))
			args = append(args, *req.DeviceType)
			argIdx++
		}
		if req.MatchType != nil {
			setClauses = append(setClauses, "match_type = $"+strconv.Itoa(argIdx))
			args = append(args, *req.MatchType)
			argIdx++
		}
		if req.MatchValue != nil {
			setClauses = append(setClauses, "match_value = $"+strconv.Itoa(argIdx))
			args = append(args, *req.MatchValue)
			argIdx++
		}
		if req.Regex != nil {
			setClauses = append(setClauses, "regex = $"+strconv.Itoa(argIdx))
			args = append(args, *req.Regex)
			argIdx++
		}
		if req.Enabled != nil {
			setClauses = append(setClauses, "enabled = $"+strconv.Itoa(argIdx))
			args = append(args, *req.Enabled)
			argIdx++
		}

		if len(setClauses) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no fields to update"})
			return
		}

		setClauses = append(setClauses, "updated_at = NOW()")
		args = append(args, id)

		query := "UPDATE parsers SET " + joinStrings(setClauses, ", ") + " WHERE id = $" + strconv.Itoa(argIdx)

		isBuiltin := false
		db.QueryRow("SELECT is_builtin FROM parsers WHERE id = $1", id).Scan(&isBuiltin)

		if isBuiltin {
			c.JSON(http.StatusForbidden, gin.H{"error": "cannot modify built-in parser"})
			return
		}

		_, err = db.Exec(query, args...)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		engine.Reload()
		c.JSON(http.StatusOK, gin.H{"message": "parser updated"})
	}
}

func DeleteParser(engine *parser.Engine) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
			return
		}

		db := engine.GetDB()

		isBuiltin := false
		db.QueryRow("SELECT is_builtin FROM parsers WHERE id = $1", id).Scan(&isBuiltin)

		if isBuiltin {
			c.JSON(http.StatusForbidden, gin.H{"error": "cannot delete built-in parser"})
			return
		}

		_, err = db.Exec("DELETE FROM parsers WHERE id = $1", id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		engine.Reload()
		c.JSON(http.StatusOK, gin.H{"message": "parser deleted"})
	}
}

func CloneParser(engine *parser.Engine) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
			return
		}

		db := engine.GetDB()

		var name, deviceType, matchType, regex string
		var description, matchValue *string
		var enabled bool
		err = db.QueryRow(
			"SELECT name, description, device_type, match_type, match_value, regex, enabled FROM parsers WHERE id = $1", id,
		).Scan(&name, &description, &deviceType, &matchType, &matchValue, &regex, &enabled)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "parser not found"})
			return
		}

		tx, err := db.Begin()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		defer tx.Rollback()

		cloneName := name + " (Copy)"
		var newID int64
		err = tx.QueryRow(`
			INSERT INTO parsers (name, description, device_type, match_type, match_value, regex, enabled, is_builtin)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id
		`, cloneName, description, deviceType, matchType, matchValue, regex, enabled, false).
			Scan(&newID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		_, err = tx.Exec(`
			INSERT INTO parsed_fields_registry (parser_id, field_name, field_label, field_type)
			SELECT $1, field_name, field_label, field_type FROM parsed_fields_registry WHERE parser_id = $2
		`, newID, id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		if err := tx.Commit(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		engine.Reload()
		c.JSON(http.StatusCreated, gin.H{"id": newID, "name": cloneName, "message": "parser cloned"})
	}
}

func TestParser(engine *parser.Engine) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req model.ParserTestRequest

		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		resp, err := engine.TestParser(req.Pattern, req.SampleLog)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, resp)
	}
}

func ReparseUnparsed(engine *parser.Engine) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req model.ReparseRequest

		if err := c.ShouldBindJSON(&req); err != nil {
			req.Limit = 10000
		}

		if req.Limit <= 0 {
			req.Limit = 10000
		}

		go func() {
			_, _ = engine.ReparseUnparsed(req.Hostname, req.From, req.To, req.Limit)
		}()

		c.JSON(http.StatusOK, gin.H{"message": "reparse started in background"})
	}
}

func ListParsedFields(engine *parser.Engine) gin.HandlerFunc {
	return func(c *gin.Context) {
		hostnamesRaw := c.Query("hostnames")
		if hostnamesRaw != "" {
			hostnames := strings.Split(hostnamesRaw, ",")
			for i, h := range hostnames {
				hostnames[i] = strings.TrimSpace(h)
			}
			fields, err := engine.GetParsedFieldsForHostnames(hostnames)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusOK, fields)
			return
		}
		fields, err := engine.GetParsedFieldRegistry()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, fields)
	}
}

func joinStrings(strs []string, sep string) string {
	result := ""
	for i, s := range strs {
		if i > 0 {
			result += sep
		}
		result += s
	}
	return result
}
package handler

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"syslog-gui/model"

	"github.com/gin-gonic/gin"
)

func StreamLogs(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		flusher, ok := c.Writer.(http.Flusher)
		if !ok {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Streaming not supported"})
			return
		}

		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.Header().Set("Cache-Control", "no-cache")
		c.Writer.Header().Set("Connection", "keep-alive")
		c.Writer.WriteHeader(http.StatusOK)
		flusher.Flush()

		since := c.DefaultQuery("since", time.Now().Format(time.RFC3339))
		hostname := c.Query("hostname")
		severity := c.Query("severity")
		search := c.Query("search")
		from := c.Query("from")
		to := c.Query("to")

		ctx := c.Request.Context()

		buildWhere := func() (string, []interface{}) {
			clauses := []string{"timestamp > $1"}
			args := []interface{}{since}
			idx := 2

			if hostname != "" {
				clauses = append(clauses, fmt.Sprintf("hostname = $%d", idx))
				args = append(args, hostname)
				idx++
			}
			if severity != "" {
				clauses = append(clauses, fmt.Sprintf("severity = $%d", idx))
				args = append(args, severity)
				idx++
			}
			if search != "" {
				clauses = append(clauses, fmt.Sprintf("(message ILIKE $%d OR raw_message ILIKE $%d)", idx, idx))
				args = append(args, "%"+search+"%")
				idx++
			}
			if from != "" {
				clauses = append(clauses, fmt.Sprintf("timestamp >= $%d", idx))
				args = append(args, from)
				idx++
			}
			if to != "" {
				clauses = append(clauses, fmt.Sprintf("timestamp <= $%d", idx))
				args = append(args, to)
				idx++
			}

			return "WHERE " + strings.Join(clauses, " AND "), args
		}

		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				whereSQL, args := buildWhere()
				query := fmt.Sprintf(
					"SELECT id, timestamp, hostname, app_name, process_id, msg_id, severity, facility, message, raw_message, parsed_fields, matched_parsers, created_at "+
						"FROM syslog_logs %s ORDER BY timestamp ASC LIMIT 100",
					whereSQL,
				)

				rows, err := db.Query(query, args...)
				if err != nil {
					continue
				}

				var logs []model.SyslogLog
				for rows.Next() {
					var l model.SyslogLog
					var rawParsed json.RawMessage
					if err := rows.Scan(
						&l.ID, &l.Timestamp, &l.Hostname, &l.AppName,
						&l.ProcessID, &l.MsgID, &l.Severity, &l.Facility,
						&l.Message, &l.RawMessage, &rawParsed, &l.MatchedParsers, &l.CreatedAt,
					); err != nil {
						continue
					}
					if len(rawParsed) > 0 {
						json.Unmarshal(rawParsed, &l.ParsedFields)
					}
					logs = append(logs, l)
				}
				rows.Close()

				if len(logs) == 0 {
					continue
				}

				data, err := json.Marshal(logs)
				if err != nil {
					continue
				}
				encoded := base64.StdEncoding.EncodeToString(data)

				fmt.Fprintf(c.Writer, "event: log\ndata: %s\n\n", encoded)
				flusher.Flush()

				since = logs[len(logs)-1].Timestamp.Format(time.RFC3339)
			}
		}
	}
}
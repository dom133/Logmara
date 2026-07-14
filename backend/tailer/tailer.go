package tailer

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"syslog-gui/model"
	"syslog-gui/parser"
)

func Start(db *sql.DB, filePath string, engine *parser.Engine) {
	log.Printf("File tailer started, watching: %s", filePath)
	batchSize := 500
	batchInterval := 2 * time.Second

	var entries []model.IngestEntry
	lastFlush := time.Now()
	var filePos int64 = 0

	for {
		f, err := os.OpenFile(filePath, os.O_RDONLY, 0644)
		if err != nil {
			log.Printf("Tailer: waiting for file %s: %v", filePath, err)
			time.Sleep(2 * time.Second)
			continue
		}

		stat, err := f.Stat()
		if err != nil {
			f.Close()
			time.Sleep(2 * time.Second)
			continue
		}

		fileSize := stat.Size()
		if filePos > fileSize {
			filePos = 0
			log.Println("Tailer: file rotated, resetting position")
		}

		if _, err := f.Seek(filePos, 0); err != nil {
			f.Close()
			time.Sleep(1 * time.Second)
			continue
		}

		scanner := bufio.NewScanner(f)
		buf := make([]byte, 0, 1024*1024)
		scanner.Buffer(buf, 1024*1024)

		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}

			var entry model.IngestEntry
if err := json.Unmarshal([]byte(line), &entry); err != nil {
			log.Printf("Tailer: invalid JSON: %v", err)
			continue
		}

			if entry.Hostname == "" {
				continue
			}

			appName := entry.AppName
			result := engine.Parse(entry.Hostname, appName, entry.Message)
			if result != nil {
				jsonData, err := json.Marshal(result.Fields)
				if err == nil {
					entry.ParsedFields = jsonData
				}
				entry.MatchedParsers = result.Parsers
			}

			entries = append(entries, entry)

			now := time.Now()
			if len(entries) >= batchSize || now.Sub(lastFlush) >= batchInterval {
				if err := flushBatch(db, entries); err != nil {
					log.Printf("Tailer: flush error: %v", err)
				}
				entries = entries[:0]
				lastFlush = now
			}
		}

		curPos, err := f.Seek(0, 2)
		if err == nil {
			filePos = curPos
		}
		f.Close()

		if len(entries) > 0 {
			if err := flushBatch(db, entries); err != nil {
				log.Printf("Tailer: flush error: %v", err)
			}
			entries = entries[:0]
		}

		time.Sleep(200 * time.Millisecond)
	}
}

func flushBatch(db *sql.DB, entries []model.IngestEntry) error {
	if len(entries) == 0 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	query := `INSERT INTO syslog_logs (timestamp, hostname, app_name, process_id, msg_id, severity, facility, message, raw_message, parsed_fields, matched_parsers)
		          VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`
	stmt, err := tx.Prepare(query)
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	ingested := 0
	for _, entry := range entries {
		ts, err := parseTimestamp(entry.Timestamp)
		if err != nil {
			ts = time.Now()
		}

		appName := nullStr(entry.AppName)
		processID := nullStr(entry.ProcessID)
		msgID := nullStr(entry.MsgID)
		facility := nullStr(entry.Facility)
		rawMsg := nullStr(entry.RawMessage)
		parsedFields := json.RawMessage("{}")
		if len(entry.ParsedFields) > 0 {
			parsedFields = entry.ParsedFields
		}

		_, err = stmt.Exec(ts, entry.Hostname, appName, processID, msgID,
			entry.Severity, facility, entry.Message, rawMsg, parsedFields, entry.MatchedParsers)
		if err != nil {
			log.Printf("Tailer: insert error: %v", err)
			continue
		}
		ingested++
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	if ingested > 0 {
		log.Printf("Tailer: flushed %d logs", ingested)
	}

	return nil
}

func parseTimestamp(s string) (time.Time, error) {
	formats := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"Jan 2 15:04:05",
		time.UnixDate,
	}

	for _, format := range formats {
		t, err := time.Parse(format, s)
		if err == nil {
			return t, nil
		}
	}

	return time.Now(), fmt.Errorf("unparseable: %s", s)
}

func nullStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
package middleware

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type etagResponseWriter struct {
	gin.ResponseWriter
	buf []byte
}

func (w *etagResponseWriter) Write(b []byte) (int, error) {
	w.buf = append(w.buf, b...)
	return w.ResponseWriter.Write(b)
}

func ETag() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method != http.MethodGet {
			c.Next()
			return
		}

		if strings.Contains(c.ContentType(), "text/event-stream") {
			c.Next()
			return
		}

		if c.Writer.Status() != 0 && c.Writer.Status() != http.StatusOK {
			c.Next()
			return
		}

		w := &etagResponseWriter{
			ResponseWriter: c.Writer,
			buf:            make([]byte, 0),
		}
		c.Writer = w

		c.Next()

		if len(w.buf) == 0 || w.buf[0] != '{' {
			return
		}

		hash := sha256.Sum256(w.buf)
		etag := fmt.Sprintf("\"%s\"", base64.RawStdEncoding.EncodeToString(hash[:16]))

		if noneMatch := c.GetHeader("If-None-Match"); noneMatch != "" {
			for _, v := range splitETags(noneMatch) {
				if v == etag {
					w.ResponseWriter.Header().Set("ETag", etag)
					w.ResponseWriter.WriteHeader(http.StatusNotModified)
					return
				}
			}
		}

		lastMod := time.Now().UTC().Format(time.RFC1123)
		if modifiedSince := c.GetHeader("If-Modified-Since"); modifiedSince != "" {
			t, err := time.Parse(time.RFC1123, modifiedSince)
			if err == nil && time.Since(t) < 60*time.Second {
				w.ResponseWriter.Header().Set("ETag", etag)
				w.ResponseWriter.Header().Set("Last-Modified", lastMod)
				w.ResponseWriter.WriteHeader(http.StatusNotModified)
				return
			}
		}

		w.ResponseWriter.Header().Set("ETag", etag)
		w.ResponseWriter.Header().Set("Last-Modified", lastMod)
		w.ResponseWriter.Header().Set("Cache-Control", "no-cache, max-age=10")
		w.ResponseWriter.Write(w.buf)
	}
}

func splitETags(s string) []string {
	var result []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		part = strings.Trim(part, "\"")
		if part != "" {
			result = append(result, fmt.Sprintf("\"%s\"", part))
		}
	}
	return result
}
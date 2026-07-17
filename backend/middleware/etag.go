package middleware

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type etagResponseWriter struct {
	gin.ResponseWriter
	buf     []byte
	written bool
}

func (w *etagResponseWriter) Write(b []byte) (int, error) {
	w.buf = append(w.buf, b...)
	w.written = true
	return len(b), nil
}

func ETag() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method != http.MethodGet {
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
			if len(w.buf) > 0 {
				w.ResponseWriter.Write(w.buf)
			}
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

		w.ResponseWriter.Header().Set("ETag", etag)
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
package middleware

import (
	"bufio"
	"compress/gzip"
	"net"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

func GzipCompress() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !strings.Contains(c.GetHeader("Accept-Encoding"), "gzip") {
			c.Next()
			return
		}

		// SSE stream: compressing it defeats the point of the per-event
		// Flush() calls the handler relies on to push data as it happens
		// rather than once enough of it has accumulated to compress well.
		if strings.HasPrefix(c.Request.URL.Path, "/api/notifications/stream") {
			c.Next()
			return
		}

		w := &gzipResponseWriter{
			ResponseWriter: c.Writer,
		}
		w.gz = gzip.NewWriter(w.ResponseWriter)
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Del("Content-Length")

		c.Writer = w
		c.Next()

		if w.gz != nil {
			w.gz.Close()
		}
	}
}

type gzipResponseWriter struct {
	gin.ResponseWriter
	gz *gzip.Writer
}

func (w *gzipResponseWriter) WriteHeader(code int) {
	w.ResponseWriter.Header().Del("Content-Length")
	w.ResponseWriter.WriteHeader(code)
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	if w.gz == nil {
		return w.ResponseWriter.Write(b)
	}
	return w.gz.Write(b)
}

func (w *gzipResponseWriter) Flush() {
	if w.gz != nil {
		w.gz.Flush()
	}
	w.ResponseWriter.(http.Flusher).Flush()
}

func (w *gzipResponseWriter) Push(target string, opts *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return nil
	}
	return pusher.Push(target, opts)
}

func (w *gzipResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, nil
	}
	return hijacker.Hijack()
}

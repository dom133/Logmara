package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
)

var sensitiveFields = map[string]bool{
	"password":         true,
	"current_password": true,
	"new_password":     true,
	"bind_password":    true,
	"bind_pass":        true,
	"jwt_secret":       true,
	"encryption_key":   true,
}

func RedactSensitive() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodPost || c.Request.Method == http.MethodPut || c.Request.Method == http.MethodPatch {
			body, err := io.ReadAll(c.Request.Body)
			if err == nil {
				c.Request.Body = io.NopCloser(bytes.NewBuffer(body))
			}
		}
		c.Next()
	}
}

func RedactMap(m map[string]interface{}) {
	for k := range m {
		if sensitiveFields[k] {
			m[k] = "***redacted***"
		}
	}
}

func RedactJSON(data []byte) ([]byte, error) {
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	RedactMap(m)
	return json.Marshal(m)
}

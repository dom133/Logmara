package middleware

import (
	"errors"
	"log/slog"
	"net/http"
	"os"

	"logmara/model"

	"github.com/gin-gonic/gin"
)

// logError logs an app error with full request context for trace correlation.
func logError(c *gin.Context, appErr *model.AppError) {
	reqID, _ := c.Get("X-Request-ID")
	fields := []any{
		"code", appErr.Code,
		"method", c.Request.Method,
		"path", c.Request.URL.Path,
		"message", appErr.Message,
		"client_ip", c.ClientIP(),
		"request_id", reqID,
	}
	slog.Error("app error", fields...)
	if appErr.Cause != nil {
		slog.Error("app error cause", "cause", appErr.Cause, "request_id", reqID)
	}
}

// isDevelopment returns true when the server runs in development mode.
// In production, detailed error messages are suppressed to avoid leaking
// internal implementation details to the client.
func isDevelopment() bool {
	return os.Getenv("GIN_MODE") == "debug" || os.Getenv("ENV") == "development"
}

func ErrorHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		for _, e := range c.Errors {
			var appErr *model.AppError
			if errors.As(e, &appErr) {
				response := gin.H{
					"error": appErr.Message,
				}

				if appErr.ErrorKey != "" {
					response["error_key"] = appErr.ErrorKey
				}

				if isDevelopment() && appErr.Details != "" {
					response["details"] = appErr.Details
				}

				logError(c, appErr)
				c.AbortWithStatusJSON(appErr.Code, response)
				return
			}
		}
	}
}

func HandleError(c *gin.Context, err error) {
	var appErr *model.AppError
	if ok := errors.As(err, &appErr); ok {
		response := gin.H{
			"error": appErr.Message,
		}

		if appErr.ErrorKey != "" {
			response["error_key"] = appErr.ErrorKey
		}

		if isDevelopment() && appErr.Details != "" {
			response["details"] = appErr.Details
		}

		logError(c, appErr)
		c.AbortWithStatusJSON(appErr.Code, response)
		return
	}

	reqID, _ := c.Get("X-Request-ID")
	slog.Error("internal server error",
		"method", c.Request.Method,
		"path", c.Request.URL.Path,
		"client_ip", c.ClientIP(),
		"request_id", reqID,
	)

	c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
		"error":     "Internal server error",
		"error_key": "error.internalServerError",
	})
}

package middleware

import (
	"errors"
	"log/slog"
	"net/http"

	"syslytics/model"

	"github.com/gin-gonic/gin"
)

func ErrorHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		for _, e := range c.Errors {
			var appErr *model.AppError
			if errors.As(e, &appErr) {
				response := gin.H{
					"error": appErr.Message,
				}

				if appErr.Details != "" {
					response["details"] = appErr.Details
				}

				slog.Error("app error", "code", appErr.Code, "method", c.Request.Method, "path", c.Request.URL.Path, "message", appErr.Message)
				if appErr.Cause != nil {
					slog.Error("app error cause", "cause", appErr.Cause)
				}

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

		if appErr.Details != "" {
			response["details"] = appErr.Details
		}

		slog.Error("app error", "code", appErr.Code, "method", c.Request.Method, "path", c.Request.URL.Path, "message", appErr.Message)
		if appErr.Cause != nil {
			slog.Error("app error cause", "cause", appErr.Cause)
		}

		c.AbortWithStatusJSON(appErr.Code, response)
		return
	}

	c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
		"error": "Internal server error",
	})
}

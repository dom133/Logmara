package handler

import (
	"database/sql"
	"net/http"

	"logmara/alertengine"
	"logmara/db"
	"logmara/middleware"
	"logmara/model"

	"github.com/gin-gonic/gin"
)

func ListAlerts(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetInt64("user_id")
		isAdmin := db.IsUserAdmin(database, userID)
		alerts, err := db.GetAllAlerts(database, isAdmin, userID)
		if err != nil {
			middleware.HandleError(c, model.NewInternalKey("alerts.listFailed", err))
			return
		}
		c.JSON(http.StatusOK, alerts)
	}
}

func CreateAlert(engine *alertengine.Engine) gin.HandlerFunc {
	database := engine.GetDB()
	return func(c *gin.Context) {
		var req model.AlertRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.HandleError(c, model.NewBadRequestKey("error.invalidRequestBody", err))
			return
		}

		// The rule-type dropdown only hides admin-only types (see
		// model.AdminOnlyRuleTypes) from non-admins in the UI - this is the
		// actual enforcement, since the API can be called directly.
		if model.IsAdminOnlyRuleType(req.RuleType) && !db.IsUserAdmin(database, c.GetInt64("user_id")) {
			middleware.HandleError(c, model.NewForbiddenKey("alerts.adminOnlyCreate", nil))
			return
		}

		var createdBy int64
		if id, ok := extractUserID(c); ok {
			createdBy = id
		}

		alert, err := db.CreateAlert(database, req, createdBy)
		if err != nil {
			middleware.HandleError(c, model.NewInternalKey("alerts.createFailed", err))
			return
		}
		engine.Reload()
		c.JSON(http.StatusCreated, alert)
	}
}

func UpdateAlert(engine *alertengine.Engine) gin.HandlerFunc {
	database := engine.GetDB()
	return func(c *gin.Context) {
		id, err := parseIDParam(c.Param("id"))
		if err != nil {
			middleware.HandleError(c, model.NewBadRequestKey("error.invalidID", nil))
			return
		}

		var req model.AlertRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.HandleError(c, model.NewBadRequestKey("error.invalidRequestBody", err))
			return
		}

		// Same enforcement as CreateAlert - also blocks a non-admin editor
		// from touching an existing admin-only rule at all, since RuleType
		// is required on every update (full-replace semantics, not partial).
		if model.IsAdminOnlyRuleType(req.RuleType) && !db.IsUserAdmin(database, c.GetInt64("user_id")) {
			middleware.HandleError(c, model.NewForbiddenKey("alerts.adminOnlyModify", nil))
			return
		}

		alert, err := db.UpdateAlert(database, id, req)
		if err != nil {
			if err == sql.ErrNoRows {
				middleware.HandleError(c, model.NewNotFoundKey("alerts.notFound", nil))
				return
			}
			middleware.HandleError(c, model.NewInternalKey("alerts.updateFailed", err))
			return
		}
		engine.Reload()
		c.JSON(http.StatusOK, alert)
	}
}

func DeleteAlert(engine *alertengine.Engine) gin.HandlerFunc {
	database := engine.GetDB()
	return func(c *gin.Context) {
		id, err := parseIDParam(c.Param("id"))
		if err != nil {
			middleware.HandleError(c, model.NewBadRequestKey("error.invalidID", nil))
			return
		}

		if err := db.DeleteAlert(database, id); err != nil {
			middleware.HandleError(c, model.NewInternalKey("alerts.deleteFailed", err))
			return
		}
		engine.Reload()
		c.JSON(http.StatusOK, gin.H{"message": "alert deleted"})
	}
}

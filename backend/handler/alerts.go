package handler

import (
	"database/sql"
	"net/http"

	"syslytics/db"
	"syslytics/middleware"
	"syslytics/model"

	"github.com/gin-gonic/gin"
)

func ListAlerts(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		alerts, err := db.GetAllAlerts(database)
		if err != nil {
			middleware.HandleError(c, model.NewInternal("Failed to list alerts", err))
			return
		}
		c.JSON(http.StatusOK, alerts)
	}
}

func CreateAlert(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req model.AlertRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.HandleError(c, model.NewBadRequest("Invalid request body", err))
			return
		}

		var createdBy int64
		if uid, ok := c.Get("user_id"); ok {
			if id, ok := uid.(int64); ok {
				createdBy = id
			}
		}

		alert, err := db.CreateAlert(database, req, createdBy)
		if err != nil {
			middleware.HandleError(c, model.NewInternal("Failed to create alert", err))
			return
		}
		c.JSON(http.StatusCreated, alert)
	}
}

func UpdateAlert(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := parseIDParam(c.Param("id"))
		if err != nil {
			middleware.HandleError(c, model.NewBadRequest("invalid id", nil))
			return
		}

		var req model.AlertRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.HandleError(c, model.NewBadRequest("Invalid request body", err))
			return
		}

		alert, err := db.UpdateAlert(database, id, req)
		if err != nil {
			if err == sql.ErrNoRows {
				middleware.HandleError(c, model.NewNotFound("Alert not found", nil))
				return
			}
			middleware.HandleError(c, model.NewInternal("Failed to update alert", err))
			return
		}
		c.JSON(http.StatusOK, alert)
	}
}

func DeleteAlert(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := parseIDParam(c.Param("id"))
		if err != nil {
			middleware.HandleError(c, model.NewBadRequest("invalid id", nil))
			return
		}

		if err := db.DeleteAlert(database, id); err != nil {
			middleware.HandleError(c, model.NewInternal("Failed to delete alert", err))
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "alert deleted"})
	}
}

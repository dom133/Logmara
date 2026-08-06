package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"logmara/util"
)

// GetRabbitMQURL returns the RabbitMQ connection URL from the environment.
func GetRabbitMQURL() gin.HandlerFunc {
	return func(c *gin.Context) {
		url := util.ResolveRabbitMQURL()
		if url == "" {
			url = "amqp://logmara:logmara@localhost:5672"
		}
		c.JSON(http.StatusOK, gin.H{"rabbitmq_url": url})
	}
}

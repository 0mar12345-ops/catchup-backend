package routes

import (
	"github.com/0mar12345-ops/internal/handlers"
	"github.com/gin-gonic/gin"
)

func SetupRoutes(router *gin.Engine) {
	pingHandler := handlers.NewPingHandler()

	api := router.Group("/api")
	{
		api.GET("/ping", pingHandler.Ping)
	}
}

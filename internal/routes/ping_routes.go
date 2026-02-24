package routes

import (
	"github.com/0mar12345-ops/internal/handlers"
	"github.com/gin-gonic/gin"
)

func registerPingRoutes(api *gin.RouterGroup) {
	pingHandler := handlers.NewPingHandler()
	api.GET("/ping", pingHandler.Ping)
}

package routes

import (
	"github.com/0mar12345-ops/internal/handlers"
	"github.com/0mar12345-ops/internal/middleware"
	"github.com/0mar12345-ops/internal/services"
	"github.com/gin-gonic/gin"
)

func registerBehaviourRoutes(api *gin.RouterGroup, deps Dependencies) {
	service := services.NewBehaviourService(deps.MongoClient, deps.DBName)
	handler := handlers.NewBehaviourHandler(service)
	authGuard := middleware.NewAuthGuard(deps.JWTSecret, deps.JWTCookieName)

	behaviour := api.Group("/behaviour")
	behaviour.Use(authGuard.RequireAuth())
	{
		behaviour.POST("", handler.CreateBehaviourLog)
		behaviour.GET("", handler.GetBehaviourLogs)
	}
}

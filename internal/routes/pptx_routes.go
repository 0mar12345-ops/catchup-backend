package routes

import (
	"github.com/0mar12345-ops/internal/handlers"
	"github.com/0mar12345-ops/internal/middleware"
	"github.com/0mar12345-ops/internal/services"
	"github.com/gin-gonic/gin"
)

func registerPptxRoutes(api *gin.RouterGroup, deps Dependencies) {
	service := services.NewPptxService(deps.MongoClient, deps.DBName, deps.Config)
	handler := handlers.NewPptxHandler(service)
	authGuard := middleware.NewAuthGuard(deps.JWTSecret, deps.JWTCookieName)

	group := api.Group("/lesson-builder")
	group.Use(authGuard.RequireAuth())
	{
		group.POST("/generate-pptx", handler.GeneratePptx)
	}
}

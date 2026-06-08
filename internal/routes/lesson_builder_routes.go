package routes

import (
	"github.com/0mar12345-ops/internal/handlers"
	"github.com/0mar12345-ops/internal/middleware"
	"github.com/0mar12345-ops/internal/services"
	"github.com/gin-gonic/gin"
)

func registerLessonBuilderRoutes(api *gin.RouterGroup, deps Dependencies) {
	service := services.NewLessonBuilderService(deps.MongoClient, deps.DBName)
	handler := handlers.NewLessonBuilderHandler(service)
	authGuard := middleware.NewAuthGuard(deps.JWTSecret, deps.JWTCookieName)

	builder := api.Group("/lesson-builder")
	builder.Use(authGuard.RequireAuth())
	{
		builder.POST("/generate", handler.GenerateLesson)
	}
}

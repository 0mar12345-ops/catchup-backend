package routes

import (
	"github.com/0mar12345-ops/internal/handlers"
	"github.com/0mar12345-ops/internal/middleware"
	"github.com/0mar12345-ops/internal/services"
	"github.com/gin-gonic/gin"
)

func RegisterTimetableRoutes(api *gin.RouterGroup, deps Dependencies) {
	service := services.NewTimetableUploadService(deps.MongoClient, deps.DBName)
	handler := handlers.NewTimetableUploadHandler(service)
	authGuard := middleware.NewAuthGuard(deps.JWTSecret, deps.JWTCookieName)

	timetable := api.Group("/timetable")
	timetable.Use(authGuard.RequireAuth())
	{
		timetable.POST("/upload", handler.UploadTimetable)
	}

	termOverview := api.Group("/term-overview")
	termOverview.Use(authGuard.RequireAuth())
	{
		termOverview.POST("/upload", handler.UploadTermOverview)
	}
}

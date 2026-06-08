package routes

import (
	"github.com/0mar12345-ops/internal/handlers"
	"github.com/0mar12345-ops/internal/middleware"
	"github.com/0mar12345-ops/internal/services"
	"github.com/gin-gonic/gin"
)

func RegisterAbsenceRoutes(api *gin.RouterGroup, deps Dependencies) {
	service := services.NewAbsenceUploadService(deps.MongoClient, deps.DBName)
	handler := handlers.NewAbsenceUploadHandler(service)
	authGuard := middleware.NewAuthGuard(deps.JWTSecret, deps.JWTCookieName)

	absences := api.Group("/absences")
	absences.Use(authGuard.RequireAuth())
	{
		absences.POST("/upload", handler.UploadAbsences)
	}
}

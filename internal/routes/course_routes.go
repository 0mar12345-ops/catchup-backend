package routes

import (
	"github.com/0mar12345-ops/internal/handlers"
	"github.com/0mar12345-ops/internal/middleware"
	"github.com/0mar12345-ops/internal/services"
	"github.com/gin-gonic/gin"
)

func registerCourseRoutes(api *gin.RouterGroup, deps Dependencies) {
	courseService := services.NewCourseService(deps.MongoClient, deps.DBName)
	courseHandler := handlers.NewCourseHandler(courseService)
	authGuard := middleware.NewAuthGuard(deps.JWTSecret, deps.JWTCookieName)

	courses := api.Group("/courses")
	courses.Use(authGuard.RequireAuth())
	{
		courses.GET("", courseHandler.ListDashboardCourses)
	}
}

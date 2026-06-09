package routes

import (
	"github.com/0mar12345-ops/internal/handlers"
	"github.com/0mar12345-ops/internal/middleware"
	"github.com/0mar12345-ops/internal/services"
	"github.com/gin-gonic/gin"
)

func registerAdminRoutes(api *gin.RouterGroup, deps Dependencies) {
	service := services.NewAdminOverviewService(deps.MongoClient, deps.DBName)
	handler := handlers.NewAdminOverviewHandler(service)
	authGuard := middleware.NewAuthGuard(deps.JWTSecret, deps.JWTCookieName)

	group := api.Group("/admin")
	group.Use(authGuard.RequireAuth())
	{
		group.GET("/overview", handler.GetOverview)
	}
}

package routes

import (
	"github.com/0mar12345-ops/internal/handlers"
	"github.com/0mar12345-ops/internal/middleware"
	"github.com/0mar12345-ops/internal/services"
	"github.com/gin-gonic/gin"
)

func registerCatchUpRoutes(api *gin.RouterGroup, deps Dependencies) {
	catchUpService := services.NewCatchUpService(deps.MongoClient, deps.DBName, deps.Config)
	catchUpHandler := handlers.NewCatchUpHandler(catchUpService)
	authGuard := middleware.NewAuthGuard(deps.JWTSecret, deps.JWTCookieName)

	catchup := api.Group("/catchup")
	catchup.Use(authGuard.RequireAuth())
	{
		catchup.POST("/generate", catchUpHandler.GenerateCatchUp)
	}
}

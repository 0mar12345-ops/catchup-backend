package routes

import (
	"github.com/0mar12345-ops/internal/handlers"
	"github.com/0mar12345-ops/internal/middleware"
	"github.com/0mar12345-ops/internal/services"
	"github.com/gin-gonic/gin"
)

func registerUserRoleRoutes(api *gin.RouterGroup, deps Dependencies) {
	userOAuthService := services.NewUserOAuthService(
		deps.GoogleClientID,
		deps.GoogleClientSecret,
		deps.GoogleRedirectURL,
		deps.GoogleOAuthState,
		deps.FrontendURL,
		deps.MongoClient,
		deps.DBName,
	)
	roleService := services.NewRoleService(deps.MongoClient, deps.DBName, userOAuthService)
	h := handlers.NewRoleHandler(roleService)
	authGuard := middleware.NewAuthGuard(deps.JWTSecret, deps.JWTCookieName)

	group := api.Group("/users")
	group.Use(authGuard.RequireAuth())
	{
		group.GET("/role", h.GetRole)
		group.POST("/detect-role", h.DetectRole)
		group.PUT("/role", middleware.RequireRole("admin"), h.UpdateRole)
	}
}

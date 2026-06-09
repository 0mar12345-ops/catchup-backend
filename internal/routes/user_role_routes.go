package routes

import (
	"github.com/0mar12345-ops/internal/handlers"
	"github.com/0mar12345-ops/internal/middleware"
	"github.com/gin-gonic/gin"
)

func registerUserRoleRoutes(api *gin.RouterGroup, deps Dependencies) {
	h := handlers.NewRoleHandler()
	authGuard := middleware.NewAuthGuard(deps.JWTSecret, deps.JWTCookieName)

	group := api.Group("/users")
	group.Use(authGuard.RequireAuth())
	{
		group.GET("/role", h.GetRole)
	}
}

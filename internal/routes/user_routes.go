package routes

import (
	"github.com/0mar12345-ops/internal/handlers"
	"github.com/0mar12345-ops/internal/middleware"
	"github.com/0mar12345-ops/internal/services"
	"github.com/gin-gonic/gin"
)

func registerUserRoutes(api *gin.RouterGroup, deps Dependencies) {
	oauthService := services.NewUserOAuthService(
		deps.GoogleClientID,
		deps.GoogleClientSecret,
		deps.GoogleRedirectURL,
		deps.GoogleOAuthState,
		deps.FrontendURL,
		deps.MongoClient,
		deps.DBName,
	)
	oauthHandler := handlers.NewUserOAuthHandler(
		oauthService,
		deps.JWTSecret,
		deps.JWTCookieName,
		deps.JWTExpiryHours,
	)
	authGuard := middleware.NewAuthGuard(deps.JWTSecret, deps.JWTCookieName)

	users := api.Group("/users")
	{
		users.POST("/check-email", oauthHandler.CheckEmail)
		users.GET("/oauth/google", oauthHandler.GoogleOAuthStart)
		users.GET("/oauth/google/callback", oauthHandler.GoogleOAuthCallback)

		protected := users.Group("")
		protected.Use(authGuard.RequireAuth())
		{
			protected.GET("/me", oauthHandler.Me)
			protected.POST("/logout", oauthHandler.Logout)
		}
	}
}

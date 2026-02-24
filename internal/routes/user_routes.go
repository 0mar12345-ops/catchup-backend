package routes

import (
	"github.com/0mar12345-ops/internal/handlers"
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

	users := api.Group("/users")
	{
		users.GET("/oauth/google", oauthHandler.GoogleOAuthStart)
		users.GET("/oauth/google/callback", oauthHandler.GoogleOAuthCallback)
	}
}

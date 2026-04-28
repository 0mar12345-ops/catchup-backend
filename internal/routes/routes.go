package routes

import (
	"github.com/0mar12345-ops/config"
	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type Dependencies struct {
	MongoClient *mongo.Client
	DBName      string
	Config      *config.Config

	GoogleClientID     string
	GoogleClientSecret string
	GoogleRedirectURL  string
	GoogleOAuthState   string
	FrontendURL        string
	JWTSecret          string
	JWTCookieName      string
	JWTExpiryHours     int
}

func SetupRoutes(router *gin.Engine, mongoClient *mongo.Client, cfg *config.Config) {
	deps := Dependencies{
		MongoClient: mongoClient,
		DBName:      cfg.MongoDBName,
		Config:      cfg,

		GoogleClientID:     cfg.GoogleClientID,
		GoogleClientSecret: cfg.GoogleClientSecret,
		GoogleRedirectURL:  cfg.GoogleRedirectURL,
		GoogleOAuthState:   cfg.GoogleOAuthState,
		FrontendURL:        cfg.FrontendURL,
		JWTSecret:          cfg.JWTSecret,
		JWTCookieName:      cfg.JWTCookieName,
		JWTExpiryHours:     cfg.JWTExpiryHours,
	}

	registerSwaggerRoutes(router)

	api := router.Group("/api")

	registerPingRoutes(api)
	registerSchoolRoutes(api, deps)
	registerUserRoutes(api, deps)
	registerCourseRoutes(api, deps)
	registerCatchUpRoutes(api, deps)
}

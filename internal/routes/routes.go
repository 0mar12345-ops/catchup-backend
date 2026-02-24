package routes

import (
	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type Dependencies struct {
	MongoClient *mongo.Client
	DBName      string
}

func SetupRoutes(router *gin.Engine, mongoClient *mongo.Client, dbName string) {
	deps := Dependencies{
		MongoClient: mongoClient,
		DBName:      dbName,
	}

	registerSwaggerRoutes(router)

	api := router.Group("/api")

	registerPingRoutes(api)
	registerSchoolRoutes(api, deps)
}

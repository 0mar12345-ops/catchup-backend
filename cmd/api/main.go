package main

import (
	"context"
	"log"
	"time"

	"github.com/0mar12345-ops/config"
	"github.com/0mar12345-ops/internal/database"
	"github.com/0mar12345-ops/internal/routes"
	"github.com/gin-gonic/gin"
)

func main() {
	cfg := config.LoadConfig()

	dbCtx, dbCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer dbCancel()

	mongoClient, err := database.ConnectMongoDB(dbCtx, cfg.MongoURI)
	if err != nil {
		log.Fatalf("Failed to connect to MongoDB: %v", err)
	}
	log.Printf("Connected to MongoDB (db: %s)", cfg.MongoDBName)

	defer func() {
		disconnectCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := database.DisconnectMongoDB(disconnectCtx, mongoClient); err != nil {
			log.Printf("Failed to disconnect MongoDB: %v", err)
		}
	}()

	gin.SetMode(cfg.GinMode)

	router := gin.Default()

	routes.SetupRoutes(router, mongoClient, cfg)

	log.Printf("Starting server on port %s...", cfg.Port)
	if err := router.Run(":" + cfg.Port); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

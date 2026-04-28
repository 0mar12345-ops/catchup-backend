package main

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/0mar12345-ops/config"
	"github.com/0mar12345-ops/internal/database"
	"github.com/0mar12345-ops/internal/routes"
	"github.com/gin-contrib/cors"
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
	router.Use(cors.New(cors.Config{
		AllowOrigins:     parseAllowedOrigins(cfg.CORSAllowedOrigins),
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	routes.SetupRoutes(router, mongoClient, cfg)

	log.Printf("Starting server on port %s...", cfg.Port)
	if err := router.Run(":" + cfg.Port); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

func parseAllowedOrigins(raw string) []string {
	parts := strings.Split(raw, ",")
	origins := make([]string, 0, len(parts))

	for _, part := range parts {
		origin := strings.TrimSpace(part)
		if origin != "" {
			origins = append(origins, origin)
		}
	}

	if len(origins) == 0 {
		return []string{"http://localhost:3000"}
	}

	return origins
}

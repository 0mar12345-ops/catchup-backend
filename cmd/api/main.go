package main

import (
	"log"

	"github.com/0mar12345-ops/config"
	"github.com/0mar12345-ops/internal/routes"
	"github.com/gin-gonic/gin"
)

func main() {
	cfg := config.LoadConfig()

	gin.SetMode(cfg.GinMode)

	router := gin.Default()

	routes.SetupRoutes(router)

	log.Printf("Starting server on port %s...", cfg.Port)
	if err := router.Run(":" + cfg.Port); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

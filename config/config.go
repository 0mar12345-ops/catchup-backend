package config

import (
	"log"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	Port        string
	GinMode     string
	MongoURI    string
	MongoDBName string

	GoogleClientID     string
	GoogleClientSecret string
	GoogleRedirectURL  string
	GoogleOAuthState   string
	FrontendURL        string

	JWTSecret      string
	JWTCookieName  string
	JWTExpiryHours int
}

func LoadConfig() *Config {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	return &Config{
		Port:        getEnv("PORT", "8080"),
		GinMode:     getEnv("GIN_MODE", "debug"),
		MongoURI:    getEnv("MONGODB_URI", "mongodb://localhost:27017"),
		MongoDBName: getEnv("MONGODB_DB", "gclass"),

		GoogleClientID:     getEnv("GOOGLE_CLIENT_ID", ""),
		GoogleClientSecret: getEnv("GOOGLE_CLIENT_SECRET", ""),
		GoogleRedirectURL:  getEnv("GOOGLE_REDIRECT_URL", "http://localhost:8080/api/users/oauth/google/callback"),
		GoogleOAuthState:   getEnv("GOOGLE_OAUTH_STATE", "gclass-ai-state"),
		FrontendURL:        getEnv("FRONTEND_URL", "http://localhost:3000"),

		JWTSecret:      getEnv("JWT_SECRET", "change-me-in-env"),
		JWTCookieName:  getEnv("JWT_COOKIE_NAME", "gclass_token"),
		JWTExpiryHours: getEnvInt("JWT_EXPIRY_HOURS", 24),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return defaultValue
	}

	return parsed
}

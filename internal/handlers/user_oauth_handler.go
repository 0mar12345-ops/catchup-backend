package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/0mar12345-ops/internal/middleware"
	"github.com/0mar12345-ops/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

type UserOAuthHandler struct {
	service       *services.UserOAuthService
	jwtSecret     []byte
	cookieName    string
	jwtExpiryHour int
}

func NewUserOAuthHandler(service *services.UserOAuthService, jwtSecret, cookieName string, jwtExpiryHour int) *UserOAuthHandler {
	if cookieName == "" {
		cookieName = "gclass_token"
	}
	if jwtExpiryHour <= 0 {
		jwtExpiryHour = 24
	}

	return &UserOAuthHandler{
		service:       service,
		jwtSecret:     []byte(jwtSecret),
		cookieName:    cookieName,
		jwtExpiryHour: jwtExpiryHour,
	}
}

func (h *UserOAuthHandler) GoogleOAuthStart(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"auth_url": h.service.GetGoogleAuthURL(),
	})
}

func (h *UserOAuthHandler) GoogleOAuthCallback(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	data, err := h.service.HandleGoogleCallback(ctx, c.Query("state"), c.Query("code"))
	if err != nil {
		switch {
		case errors.Is(err, services.ErrInvalidOAuthState), errors.Is(err, services.ErrMissingOAuthCode):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	tokenString, err := h.createJWT(data)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create jwt token"})
		return
	}

	maxAge := h.jwtExpiryHour * 60 * 60
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(h.cookieName, tokenString, maxAge, "/", "", false, true)

	frontendURL := h.service.FrontendURL()
	if frontendURL == "" {
		c.JSON(http.StatusOK, gin.H{"message": "oauth success"})
		return
	}

	c.Redirect(http.StatusFound, frontendURL)
}

func (h *UserOAuthHandler) Me(c *gin.Context) {
	claims, ok := middleware.GetAuthClaims(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	username, _ := claims["name"].(string)
	role, _ := claims["role"].(string)
	if role == "" {
		role = "teacher"
	}

	c.JSON(http.StatusOK, gin.H{
		"username": username,
		"role":     role,
	})
}

func (h *UserOAuthHandler) Logout(c *gin.Context) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(h.cookieName, "", -1, "/", "", false, true)
	c.JSON(http.StatusOK, gin.H{"message": "logged out"})
}

func (h *UserOAuthHandler) createJWT(data *services.OAuthSyncResult) (string, error) {
	now := time.Now().UTC()
	expiresAt := now.Add(time.Duration(h.jwtExpiryHour) * time.Hour)

	claims := jwt.MapClaims{
		"sub":           data.UserID,
		"school_id":     data.SchoolID,
		"email":         data.TeacherEmail,
		"name":          data.TeacherName,
		"role":          "teacher",
		"courses_sync":  data.CoursesSynced,
		"students_sync": data.StudentsSynced,
		"iat":           now.Unix(),
		"exp":           expiresAt.Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(h.jwtSecret)
}

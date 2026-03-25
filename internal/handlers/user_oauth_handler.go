package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/0mar12345-ops/internal/middleware"
	"github.com/0mar12345-ops/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type UserOAuthHandler struct {
	service       *services.UserOAuthService
	jwtSecret     []byte
	cookieName    string
	jwtExpiryHour int
	cookieDomain  string
	cookieSecure  bool
}

func NewUserOAuthHandler(service *services.UserOAuthService, jwtSecret, cookieName string, jwtExpiryHour int) *UserOAuthHandler {
	if cookieName == "" {
		cookieName = "gclass_token"
	}
	if jwtExpiryHour <= 0 {
		jwtExpiryHour = 24
	}

	cookieDomain, cookieSecure := deriveCookieSettings(service.FrontendURL())

	return &UserOAuthHandler{
		service:       service,
		jwtSecret:     []byte(jwtSecret),
		cookieName:    cookieName,
		jwtExpiryHour: jwtExpiryHour,
		cookieDomain:  cookieDomain,
		cookieSecure:  cookieSecure,
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
	c.SetCookie(h.cookieName, tokenString, maxAge, "/", h.cookieDomain, h.cookieSecure, true)

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

	userID, _ := claims["sub"].(string)
	schoolID, _ := claims["school_id"].(string)
	if userID == "" || schoolID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	meData, err := h.service.GetMeData(ctx, userID, schoolID)
	if err != nil {
		switch {
		case errors.Is(err, services.ErrInvalidAuthUserID), errors.Is(err, services.ErrInvalidAuthSchoolID):
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch user profile"})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"username": meData.User.Name,
		"role":     meData.User.Role,
	})
}

func (h *UserOAuthHandler) Logout(c *gin.Context) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(h.cookieName, "", -1, "/", h.cookieDomain, h.cookieSecure, true)
	c.JSON(http.StatusOK, gin.H{"message": "logged out"})
}

// GetOAuthStatus returns the current OAuth token status for the logged-in user
func (h *UserOAuthHandler) GetOAuthStatus(c *gin.Context) {
	claims, ok := middleware.GetAuthClaims(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	userID, _ := claims["sub"].(string)
	schoolID, _ := claims["school_id"].(string)
	if userID == "" || schoolID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	userOID, err := bson.ObjectIDFromHex(userID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}

	schoolOID, err := bson.ObjectIDFromHex(schoolID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid school id"})
		return
	}

	oauthCred, err := h.service.GetOAuthCredential(ctx, userOID, schoolOID)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"status":       "not_found",
			"has_oauth":    false,
			"needs_reauth": true,
			"message":      "No OAuth credentials found",
		})
		return
	}

	// Default to valid if status is empty (for backward compatibility)
	status := oauthCred.Status
	if status == "" {
		status = "valid"
	}

	c.JSON(http.StatusOK, gin.H{
		"status":       status,
		"has_oauth":    true,
		"needs_reauth": status == "invalid",
		"granted_at":   oauthCred.GrantedAt,
		"scopes_count": len(oauthCred.Scopes),
	})
}

// GetReauthorizeURL returns the Google OAuth URL for re-authorization
func (h *UserOAuthHandler) GetReauthorizeURL(c *gin.Context) {
	claims, ok := middleware.GetAuthClaims(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	userID, _ := claims["sub"].(string)
	schoolID, _ := claims["school_id"].(string)
	if userID == "" || schoolID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	// Return the standard OAuth URL (prompt=select_account)
	authURL := h.service.GetGoogleAuthURL()

	c.JSON(http.StatusOK, gin.H{
		"auth_url": authURL,
	})
}

func deriveCookieSettings(frontendURL string) (domain string, secure bool) {
	parsed, err := url.Parse(frontendURL)
	if err != nil {
		return "", false
	}

	host := parsed.Hostname()
	if host == "" {
		return "", parsed.Scheme == "https"
	}

	if host == "localhost" {
		return "", parsed.Scheme == "https"
	}

	if strings.HasPrefix(host, "127.") || host == "::1" {
		return "", parsed.Scheme == "https"
	}

	return host, parsed.Scheme == "https"
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

func (h *UserOAuthHandler) SyncCourses(c *gin.Context) {
	claims, ok := middleware.GetAuthClaims(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	userID, _ := claims["sub"].(string)
	schoolID, _ := claims["school_id"].(string)
	if userID == "" || schoolID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	result, err := h.service.SyncCoursesForUser(ctx, userID, schoolID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

func (h *UserOAuthHandler) SyncCourseStudents(c *gin.Context) {
	claims, ok := middleware.GetAuthClaims(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	userID, _ := claims["sub"].(string)
	schoolID, _ := claims["school_id"].(string)
	if userID == "" || schoolID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	courseID := c.Param("courseId")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	result, err := h.service.SyncCourseStudents(ctx, courseID, userID, schoolID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

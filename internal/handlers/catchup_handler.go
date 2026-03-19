package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/0mar12345-ops/internal/middleware"
	"github.com/0mar12345-ops/internal/services"
	"github.com/gin-gonic/gin"
)

type CatchUpHandler struct {
	service *services.CatchUpService
}

func NewCatchUpHandler(service *services.CatchUpService) *CatchUpHandler {
	return &CatchUpHandler{service: service}
}

func (h *CatchUpHandler) GenerateCatchUp(c *gin.Context) {
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

	var req services.GenerateCatchUpRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()

	result, err := h.service.GenerateCatchUpForStudents(ctx, req, userID, schoolID)
	if err != nil {
		switch {
		case errors.Is(err, services.ErrInvalidCatchUpCourseID):
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid course id"})
		case errors.Is(err, services.ErrCourseNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "course not found"})
		case errors.Is(err, services.ErrNoContentFound):
			c.JSON(http.StatusBadRequest, gin.H{"error": "no content found for the specified date"})
		case errors.Is(err, services.ErrInsufficientContent):
			c.JSON(http.StatusBadRequest, gin.H{"error": "insufficient content to generate catch-up lesson"})
		case errors.Is(err, services.ErrOAuthTokenInvalid):
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":        "oauth_invalid",
				"message":      "Your Google Classroom authorization has expired. Please re-authorize your account.",
				"needs_reauth": true,
			})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate catch-up"})
		}
		return
	}

	c.JSON(http.StatusOK, result)
}

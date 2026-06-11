package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/0mar12345-ops/internal/middleware"
	"github.com/0mar12345-ops/internal/services"
	"github.com/gin-gonic/gin"
)

type CourseSelectionHandler struct {
	service *services.UserOAuthService
}

func NewCourseSelectionHandler(service *services.UserOAuthService) *CourseSelectionHandler {
	return &CourseSelectionHandler{service: service}
}

func (h *CourseSelectionHandler) GetAvailableCourses(c *gin.Context) {
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

	previews, err := h.service.GetAvailableCourses(ctx, userID, schoolID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"courses": previews})
}

func (h *CourseSelectionHandler) ImportCourses(c *gin.Context) {
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

	var body struct {
		CourseIDs []string `json:"course_ids"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || len(body.CourseIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "course_ids is required and must not be empty"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()

	result, err := h.service.ImportSelectedCourses(ctx, userID, schoolID, body.CourseIDs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

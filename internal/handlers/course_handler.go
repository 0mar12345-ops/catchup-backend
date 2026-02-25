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

type CourseHandler struct {
	service *services.CourseService
}

func NewCourseHandler(service *services.CourseService) *CourseHandler {
	return &CourseHandler{service: service}
}

func (h *CourseHandler) ListDashboardCourses(c *gin.Context) {
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

	courses, err := h.service.ListDashboardCourses(ctx, userID, schoolID)
	if err != nil {
		switch {
		case errors.Is(err, services.ErrInvalidCourseUserID), errors.Is(err, services.ErrInvalidCourseSchoolID):
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch dashboard courses"})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"courses": courses})
}

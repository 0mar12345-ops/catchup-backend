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

type CatchUpViewHandler struct {
	service *services.CatchUpViewService
}

func NewCatchUpViewHandler(service *services.CatchUpViewService) *CatchUpViewHandler {
	return &CatchUpViewHandler{service: service}
}

func (h *CatchUpViewHandler) GetStudentCatchUpLessons(c *gin.Context) {
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
	studentID := c.Param("studentId")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	lessons, err := h.service.GetStudentCatchUpLessons(ctx, courseID, studentID, userID, schoolID)
	if err != nil {
		switch {
		case errors.Is(err, services.ErrUnauthorizedAccess):
			c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch catch-up lessons"})
		}
		return
	}

	c.JSON(http.StatusOK, lessons)
}

func (h *CatchUpViewHandler) GetCatchUpLesson(c *gin.Context) {
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
	studentID := c.Param("studentId")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	lesson, err := h.service.GetCatchUpLessonForReview(ctx, courseID, studentID, userID, schoolID)
	if err != nil {
		switch {
		case errors.Is(err, services.ErrCatchUpLessonNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "catch-up lesson not found"})
		case errors.Is(err, services.ErrUnauthorizedAccess):
			c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch catch-up lesson"})
		}
		return
	}

	c.JSON(http.StatusOK, lesson)
}

func (h *CatchUpViewHandler) DeliverCatchUpLesson(c *gin.Context) {
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

	lessonID := c.Param("lessonId")

	var req struct {
		DueDate *time.Time `json:"due_date"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	err := h.service.DeliverCatchUpLesson(ctx, lessonID, userID, schoolID, req.DueDate)
	if err != nil {
		switch {
		case errors.Is(err, services.ErrCatchUpLessonNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "catch-up lesson not found"})
		case errors.Is(err, services.ErrUnauthorizedAccess):
			c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
		default:
			// Log the detailed error for debugging
			c.Error(err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to deliver lesson", "details": err.Error()})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "lesson delivered successfully"})
}

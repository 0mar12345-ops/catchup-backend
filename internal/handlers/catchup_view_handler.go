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

func (h *CatchUpViewHandler) GetCatchUpLessonById(c *gin.Context) {
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

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	lesson, err := h.service.GetCatchUpLessonByID(ctx, lessonID, userID, schoolID)
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
		Title   *string    `json:"title"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	err := h.service.DeliverCatchUpLesson(ctx, lessonID, userID, schoolID, req.DueDate, req.Title)
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

func (h *CatchUpViewHandler) RegenerateCatchUpLesson(c *gin.Context) {
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
		RegenerationType string  `json:"regeneration_type"` // "full", "explanation", "quiz"
		CustomPrompt     *string `json:"custom_prompt"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	// Validate regeneration type
	if req.RegenerationType != "full" && req.RegenerationType != "explanation" && req.RegenerationType != "quiz" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid regeneration_type. Must be 'full', 'explanation', or 'quiz'"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()

	lesson, err := h.service.RegenerateCatchUpLesson(ctx, lessonID, userID, schoolID, req.RegenerationType, req.CustomPrompt)
	if err != nil {
		switch {
		case errors.Is(err, services.ErrCatchUpLessonNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "catch-up lesson not found"})
		case errors.Is(err, services.ErrUnauthorizedAccess):
			c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
		default:
			c.Error(err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to regenerate lesson", "details": err.Error()})
		}
		return
	}

	c.JSON(http.StatusOK, lesson)
}

func (h *CatchUpViewHandler) GetCourseStats(c *gin.Context) {
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

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	stats, err := h.service.GetCourseStats(ctx, courseID, userID, schoolID)
	if err != nil {
		switch {
		case errors.Is(err, services.ErrUnauthorizedAccess):
			c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch course stats"})
		}
		return
	}

	c.JSON(http.StatusOK, stats)
}

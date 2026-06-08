package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/0mar12345-ops/internal/middleware"
	"github.com/0mar12345-ops/internal/services"
	"github.com/gin-gonic/gin"
)

type LessonBuilderHandler struct {
	service *services.LessonBuilderService
}

func NewLessonBuilderHandler(service *services.LessonBuilderService) *LessonBuilderHandler {
	return &LessonBuilderHandler{service: service}
}

func (h *LessonBuilderHandler) GenerateLesson(c *gin.Context) {
	claims, ok := middleware.GetAuthClaims(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	teacherID, _ := claims["sub"].(string)
	schoolID, _ := claims["school_id"].(string)
	if teacherID == "" || schoolID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var input struct {
		CourseID   string `json:"course_id" binding:"required"`
		WeekNumber int    `json:"week_number"`
		Date       string `json:"date"`
		Topic      string `json:"topic" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "course_id, topic, and a valid payload are required"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()

	lesson, err := h.service.GenerateLesson(ctx, teacherID, schoolID, input.CourseID, input.WeekNumber, input.Date, input.Topic)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, lesson)
}

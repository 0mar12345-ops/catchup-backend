package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/0mar12345-ops/internal/middleware"
	"github.com/0mar12345-ops/internal/services"
	"github.com/gin-gonic/gin"
)

type PptxHandler struct {
	service *services.PptxService
}

func NewPptxHandler(service *services.PptxService) *PptxHandler {
	return &PptxHandler{service: service}
}

func (h *PptxHandler) GeneratePptx(c *gin.Context) {
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
		Topic      string `json:"topic" binding:"required"`
		WeekNumber int    `json:"week_number"`
		Date       string `json:"date"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "course_id and topic are required"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()

	buf, filename, err := h.service.GeneratePptx(
		ctx,
		teacherID, schoolID,
		input.CourseID,
		input.WeekNumber,
		input.Date,
		input.Topic,
	)
	if err != nil {
		switch {
		case errors.Is(err, services.ErrOpenAINotConfigured):
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI generation is not configured"})
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		}
		return
	}

	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	c.Data(
		http.StatusOK,
		"application/vnd.openxmlformats-officedocument.presentationml.presentation",
		buf.Bytes(),
	)
}

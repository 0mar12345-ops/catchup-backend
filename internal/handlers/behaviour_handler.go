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

type BehaviourHandler struct {
	service *services.BehaviourService
}

func NewBehaviourHandler(service *services.BehaviourService) *BehaviourHandler {
	return &BehaviourHandler{service: service}
}

func (h *BehaviourHandler) CreateBehaviourLog(c *gin.Context) {
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
		CourseID     string `json:"course_id" binding:"required"`
		CourseName   string `json:"course_name"`
		StudentEmail string `json:"student_email"`
		StudentName  string `json:"student_name" binding:"required"`
		Type         string `json:"type" binding:"required"`
		Category     string `json:"category" binding:"required"`
		Notes        string `json:"notes"`
		Date         string `json:"date"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "course_id, student_email, student_name, type, and category are required"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	log, err := h.service.CreateBehaviourLog(ctx, services.CreateBehaviourLogInput{
		TeacherID:    teacherID,
		SchoolID:     schoolID,
		CourseID:     input.CourseID,
		CourseName:   input.CourseName,
		StudentEmail: input.StudentEmail,
		StudentName:  input.StudentName,
		Type:         input.Type,
		Category:     input.Category,
		Notes:        input.Notes,
		Date:         input.Date,
	})
	if err != nil {
		switch {
		case errors.Is(err, services.ErrInvalidBehaviourCourseID):
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid course_id"})
		case errors.Is(err, services.ErrInvalidBehaviourType):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		case errors.Is(err, services.ErrBehaviourCategoryRequired):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		case errors.Is(err, services.ErrBehaviourStudentRequired):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create behaviour log"})
		}
		return
	}

	c.JSON(http.StatusCreated, log)
}

func (h *BehaviourHandler) GetBehaviourLogs(c *gin.Context) {
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

	typeFilter := c.Query("type")
	courseID := c.Query("course_id")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	if courseID != "" {
		result, err := h.service.GetBehaviourLogsByCourse(ctx, teacherID, schoolID, courseID)
		if err != nil {
			switch {
			case errors.Is(err, services.ErrInvalidBehaviourCourseID):
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid course_id"})
			default:
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch behaviour logs"})
			}
			return
		}
		c.JSON(http.StatusOK, result)
		return
	}

	result, err := h.service.GetBehaviourLogs(ctx, services.GetBehaviourLogsInput{
		TeacherID: teacherID,
		SchoolID:  schoolID,
		Type:      typeFilter,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch behaviour logs"})
		return
	}

	c.JSON(http.StatusOK, result)
}

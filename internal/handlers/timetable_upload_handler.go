package handlers

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/0mar12345-ops/internal/middleware"
	"github.com/0mar12345-ops/internal/services"
	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// TimetableUploadHandler handles timetable and term overview uploads.
type TimetableUploadHandler struct {
	service *services.TimetableUploadService
}

func NewTimetableUploadHandler(service *services.TimetableUploadService) *TimetableUploadHandler {
	return &TimetableUploadHandler{service: service}
}

func (h *TimetableUploadHandler) UploadTimetable(c *gin.Context) {
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

	teacherOID, err := bson.ObjectIDFromHex(teacherID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid teacher id"})
		return
	}
	schoolOID, err := bson.ObjectIDFromHex(schoolID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid school id"})
		return
	}

	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file is required"})
		return
	}

	opened, err := file.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to open uploaded file"})
		return
	}
	defer opened.Close()

	content, err := contextReader(opened)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read uploaded file"})
		return
	}

	termLabel := c.PostForm("term_label")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	record, err := h.service.UploadTimetable(ctx, teacherOID, schoolOID, termLabel, file.Filename, content)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, record)
}

func (h *TimetableUploadHandler) UploadTermOverview(c *gin.Context) {
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

	teacherOID, err := bson.ObjectIDFromHex(teacherID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid teacher id"})
		return
	}
	schoolOID, err := bson.ObjectIDFromHex(schoolID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid school id"})
		return
	}

	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file is required"})
		return
	}

	opened, err := file.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to open uploaded file"})
		return
	}
	defer opened.Close()

	content, err := contextReader(opened)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read uploaded file"})
		return
	}

	termLabel := c.PostForm("term_label")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	record, err := h.service.UploadTermOverview(ctx, teacherOID, schoolOID, termLabel, file.Filename, content)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, record)
}

func contextReader(r interface{ Read([]byte) (int, error) }) ([]byte, error) {
	return io.ReadAll(r)
}

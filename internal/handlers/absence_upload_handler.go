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

// AbsenceUploadHandler handles CSV absence uploads.
type AbsenceUploadHandler struct {
	service *services.AbsenceUploadService
}

func NewAbsenceUploadHandler(service *services.AbsenceUploadService) *AbsenceUploadHandler {
	return &AbsenceUploadHandler{service: service}
}

func (h *AbsenceUploadHandler) UploadAbsences(c *gin.Context) {
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

	content, err := io.ReadAll(opened)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read uploaded file"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	result, err := h.service.UploadAbsences(ctx, teacherOID, schoolOID, file.Filename, content)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, result)
}

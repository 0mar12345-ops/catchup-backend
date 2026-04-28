package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/0mar12345-ops/internal/services"
	"github.com/gin-gonic/gin"
)

type SchoolHandler struct {
	service *services.SchoolService
}

func NewSchoolHandler(service *services.SchoolService) *SchoolHandler {
	return &SchoolHandler{service: service}
}

type createSchoolRequest struct {
	Name     string `json:"name" binding:"required"`
	Code     string `json:"code" binding:"required"`
	Domain   string `json:"domain"`
	Timezone string `json:"timezone"`
	IsActive *bool  `json:"is_active"`
}

type updateSchoolRequest struct {
	Name     *string `json:"name"`
	Code     *string `json:"code"`
	Domain   *string `json:"domain"`
	Timezone *string `json:"timezone"`
	IsActive *bool   `json:"is_active"`
}

func (h *SchoolHandler) CreateSchool(c *gin.Context) {
	var req createSchoolRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	school, err := h.service.CreateSchool(ctx, services.CreateSchoolInput{
		Name:     req.Name,
		Code:     req.Code,
		Domain:   req.Domain,
		Timezone: req.Timezone,
		IsActive: isActive,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create school"})
		return
	}

	c.JSON(http.StatusCreated, school)
}

func (h *SchoolHandler) ListSchools(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	schools, err := h.service.ListSchools(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch schools"})
		return
	}

	c.JSON(http.StatusOK, schools)
}

func (h *SchoolHandler) GetSchool(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	school, err := h.service.GetSchoolByID(ctx, c.Param("id"))
	if err != nil {
		h.handleSchoolError(c, err)
		return
	}

	c.JSON(http.StatusOK, school)
}

func (h *SchoolHandler) UpdateSchool(c *gin.Context) {
	var req updateSchoolRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	school, err := h.service.UpdateSchool(ctx, c.Param("id"), services.UpdateSchoolInput{
		Name:     req.Name,
		Code:     req.Code,
		Domain:   req.Domain,
		Timezone: req.Timezone,
		IsActive: req.IsActive,
	})
	if err != nil {
		h.handleSchoolError(c, err)
		return
	}

	c.JSON(http.StatusOK, school)
}

func (h *SchoolHandler) DeleteSchool(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	err := h.service.DeleteSchool(ctx, c.Param("id"))
	if err != nil {
		h.handleSchoolError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "school deleted"})
}

func (h *SchoolHandler) handleSchoolError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, services.ErrInvalidSchoolID):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, services.ErrSchoolNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	}
}

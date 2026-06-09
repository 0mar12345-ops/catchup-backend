package handlers

import (
	"net/http"

	"github.com/0mar12345-ops/internal/middleware"
	"github.com/gin-gonic/gin"
)

type RoleHandler struct{}

func NewRoleHandler() *RoleHandler { return &RoleHandler{} }

func (h *RoleHandler) GetRole(c *gin.Context) {
	claims, ok := middleware.GetAuthClaims(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	role, _ := claims["role"].(string)
	c.JSON(http.StatusOK, gin.H{"role": role})
}

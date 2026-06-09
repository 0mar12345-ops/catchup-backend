package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// RequireRole returns middleware that allows only users whose JWT "role" claim
// matches one of the given roles. Must be chained after RequireAuth.
func RequireRole(roles ...string) gin.HandlerFunc {
	allowed := make(map[string]struct{}, len(roles))
	for _, r := range roles {
		allowed[r] = struct{}{}
	}
	return func(c *gin.Context) {
		claims, ok := GetAuthClaims(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		role, _ := claims["role"].(string)
		if _, ok := allowed[role]; !ok {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden: insufficient role"})
			return
		}
		c.Next()
	}
}

package middleware

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const AuthClaimsContextKey = "auth_claims"

type AuthGuard struct {
	jwtSecret  []byte
	cookieName string
}

func NewAuthGuard(jwtSecret, cookieName string) *AuthGuard {
	if cookieName == "" {
		cookieName = "gclass_token"
	}

	return &AuthGuard{
		jwtSecret:  []byte(jwtSecret),
		cookieName: cookieName,
	}
}

func (g *AuthGuard) RequireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, err := g.parseClaimsFromCookie(c)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}

		c.Set(AuthClaimsContextKey, claims)
		c.Next()
	}
}

func GetAuthClaims(c *gin.Context) (jwt.MapClaims, bool) {
	v, ok := c.Get(AuthClaimsContextKey)
	if !ok {
		return nil, false
	}

	claims, ok := v.(jwt.MapClaims)
	return claims, ok
}

func (g *AuthGuard) parseClaimsFromCookie(c *gin.Context) (jwt.MapClaims, error) {
	tokenString, err := c.Cookie(g.cookieName)
	if err != nil || strings.TrimSpace(tokenString) == "" {
		return nil, errors.New("missing token")
	}

	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return g.jwtSecret, nil
	})
	if err != nil || !token.Valid {
		return nil, errors.New("invalid token")
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("invalid claims")
	}

	return claims, nil
}

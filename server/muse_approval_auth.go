package server

import (
	"crypto/subtle"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

func museApprovalAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")

		host, _, err := net.SplitHostPort(c.Request.RemoteAddr)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusForbidden,
				gin.H{"error": "local connection required"})
			return
		}

		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			c.AbortWithStatusJSON(http.StatusForbidden,
				gin.H{"error": "remote approval access forbidden"})
			return
		}

		secret := os.Getenv("OWNBOT_MUSE_APPROVAL_TOKEN")
		if len(secret) < 32 {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable,
				gin.H{"error": "approval authentication not configured"})
			return
		}

		header := c.GetHeader("Authorization")
		const prefix = "Bearer "

		if !strings.HasPrefix(header, prefix) ||
			subtle.ConstantTimeCompare(
				[]byte(strings.TrimPrefix(header, prefix)),
				[]byte(secret),
			) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized,
				gin.H{"error": "invalid approval credentials"})
			return
		}

		c.Next()
	}
}

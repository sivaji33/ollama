package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestMuseApprovalAuthGate(t *testing.T) {
	secret := strings.Repeat("X", 40)
	t.Setenv("OWNBOT_MUSE_APPROVAL_TOKEN", secret)

	router := gin.New()
	allow := func(c *gin.Context) {
		c.Status(http.StatusOK)
	}

	router.GET("/api/agent/approvals", museApprovalAuth(), allow)
	router.POST("/api/agent/approvals/:id", museApprovalAuth(), allow)

	cases := []struct {
		name, method, path, remote, auth string
		want                             int
	}{
		{
			"local authorized GET",
			"GET", "/api/agent/approvals",
			"127.0.0.1:5000", "Bearer " + secret, 200,
		},
		{
			"local unauthorized GET",
			"GET", "/api/agent/approvals",
			"127.0.0.1:5000", "", 401,
		},
		{
			"remote authorized denied",
			"POST", "/api/agent/approvals/test",
			"192.0.2.10:5000", "Bearer " + secret, 403,
		},
		{
			"local wrong token denied",
			"POST", "/api/agent/approvals/test",
			"127.0.0.1:5000", "Bearer wrong", 401,
		},
		{
			"local authorized POST",
			"POST", "/api/agent/approvals/test",
			"127.0.0.1:5000", "Bearer " + secret, 200,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, tc.path, nil)
			request.RemoteAddr = tc.remote
			request.Header.Set("Authorization", tc.auth)

			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != tc.want {
				t.Fatalf("status=%d want=%d",
					response.Code, tc.want)
			}
		})
	}

	t.Setenv("OWNBOT_MUSE_APPROVAL_TOKEN", "")

	request := httptest.NewRequest(
		http.MethodGet, "/api/agent/approvals", nil)
	request.RemoteAddr = "127.0.0.1:5000"
	request.Header.Set("Authorization", "Bearer "+secret)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing token status=%d, want 503", response.Code)
	}
}

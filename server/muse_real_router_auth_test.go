package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMUSEProductionRouterAuthentication(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	token := strings.Repeat("T", 40)
	t.Setenv("OWNBOT_MUSE_APPROVAL_TOKEN", token)

	router, err := (&Server{}).GenerateRoutes()
	if err != nil {
		t.Fatalf("GenerateRoutes: %v", err)
	}

	routes := []struct {
		method string
		path   string
	}{
		{"POST", "/api/agent/run"},
		{"GET", "/api/agent/context"},
		{"GET", "/api/agent/approvals"},
		{"POST", "/api/agent/approvals/test-id"},
		{"POST", "/api/agent/session"},
		{"POST", "/api/agent/session/test-id/continue"},
		{"POST", "/api/agent/session/test-id/cancel"},
		{"GET", "/api/agent/session/test-id"},
		{"GET", "/api/agent/session/test-id/events"},
		{"GET", "/api/agent/session/test-id/diff"},
		{"POST", "/api/self-development"},
		{"GET", "/api/self-development/test-id"},
		{"POST", "/api/self-development/test-id/cancel"},
	}

	scenarios := []struct {
		name       string
		remoteAddr string
		auth       string
		want       int
	}{
		{
			name:       "missing_token",
			remoteAddr: "127.0.0.1:5000",
			auth:       "",
			want:       http.StatusUnauthorized,
		},
		{
			name:       "incorrect_token",
			remoteAddr: "127.0.0.1:5000",
			auth:       "Bearer incorrect",
			want:       http.StatusUnauthorized,
		},
		{
			name:       "remote_with_valid_token",
			remoteAddr: "192.0.2.10:5000",
			auth:       "Bearer " + token,
			want:       http.StatusForbidden,
		},
	}

	for _, route := range routes {
		t.Run(route.method+"_"+route.path, func(t *testing.T) {
			for _, scenario := range scenarios {
				t.Run(scenario.name, func(t *testing.T) {
					request := httptest.NewRequest(
						route.method,
						"http://127.0.0.1"+route.path,
						nil,
					)

					request.RemoteAddr = scenario.remoteAddr

					if scenario.auth != "" {
						request.Header.Set(
							"Authorization",
							scenario.auth,
						)
					}

					// A spoofed forwarding header must not
					// override the actual remote connection.
					request.Header.Set(
						"X-Forwarded-For",
						"127.0.0.1",
					)

					response := httptest.NewRecorder()
					router.ServeHTTP(response, request)

					if response.Code != scenario.want {
						t.Fatalf(
							"%s %s: status=%d want=%d body=%s",
							route.method,
							route.path,
							response.Code,
							scenario.want,
							response.Body.String(),
						)
					}
				})
			}
		})
	}

	t.Run("authorized_local_approval_list", func(t *testing.T) {
		request := httptest.NewRequest(
			http.MethodGet,
			"http://127.0.0.1/api/agent/approvals",
			nil,
		)

		request.RemoteAddr = "127.0.0.1:5000"
		request.Header.Set(
			"Authorization",
			"Bearer "+token,
		)

		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)

		if response.Code != http.StatusOK {
			t.Fatalf(
				"authorized request: status=%d body=%s",
				response.Code,
				response.Body.String(),
			)
		}
	})

	t.Run("missing_server_configuration", func(t *testing.T) {
		t.Setenv("OWNBOT_MUSE_APPROVAL_TOKEN", "")

		request := httptest.NewRequest(
			http.MethodGet,
			"http://127.0.0.1/api/agent/approvals",
			nil,
		)

		request.RemoteAddr = "127.0.0.1:5000"
		request.Header.Set(
			"Authorization",
			"Bearer "+token,
		)

		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)

		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf(
				"missing configuration: status=%d want=503",
				response.Code,
			)
		}
	})

	t.Log("PASS: all 13 sensitive routes reject unauthorized requests")
	t.Log("PASS: valid local credentials accepted")
	t.Log("PASS: missing server token fails closed")
}

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ollama/ollama/internal/muse"
)

func TestMUSEApprovalHTTPRoundTrip(t *testing.T) {
	router := gin.New()
	server := &Server{}

	router.GET(
		"/api/agent/approvals",
		server.AgentPendingApprovalsHandler,
	)
	router.POST(
		"/api/agent/approvals/:id",
		server.AgentApprovalDecisionHandler,
	)

	for _, decision := range []muse.Decision{
		muse.DecisionAllow,
		muse.DecisionDeny,
	} {
		t.Run(string(decision), func(t *testing.T) {
			item := agentApprovalStore.Request(muse.Request{
				SessionID: t.Name(),
				Action:    muse.ActionWriteFile,
				Target:    "test.txt",
			})

			t.Cleanup(func() {
				_ = agentApprovalStore.Decide(
					item.ID,
					muse.DecisionDeny,
				)
			})

			// Confirm the approval is visible via HTTP.
			pendingResponse := httptest.NewRecorder()
			pendingRequest := httptest.NewRequest(
				http.MethodGet,
				"/api/agent/approvals",
				nil,
			)

			router.ServeHTTP(pendingResponse, pendingRequest)

			if pendingResponse.Code != http.StatusOK {
				t.Fatalf(
					"pending endpoint status: %d",
					pendingResponse.Code,
				)
			}

			var pending struct {
				Approvals []muse.Approval `json:"approvals"`
			}

			if err := json.Unmarshal(
				pendingResponse.Body.Bytes(),
				&pending,
			); err != nil {
				t.Fatal(err)
			}

			found := false
			for _, approval := range pending.Approvals {
				if approval.ID == item.ID {
					found = true
					break
				}
			}

			if !found {
				t.Fatal("approval not visible through HTTP")
			}

			// Send an HTTP decision while Wait is active.
			responseCh := make(chan *httptest.ResponseRecorder, 1)

			go func() {
				time.Sleep(30 * time.Millisecond)

				body, _ := json.Marshal(map[string]string{
					"decision": string(decision),
				})

				request := httptest.NewRequest(
					http.MethodPost,
					"/api/agent/approvals/"+item.ID,
					bytes.NewReader(body),
				)
				request.Header.Set(
					"Content-Type",
					"application/json",
				)

				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)
				responseCh <- response
			}()

			ctx, cancel := context.WithTimeout(
				context.Background(),
				3*time.Second,
			)
			defer cancel()

			actual, err := agentApprovalStore.Wait(
				ctx,
				item,
				muse.PolicyFor(muse.ActionWriteFile),
			)

			response := <-responseCh

			if response.Code != http.StatusOK {
				t.Fatalf(
					"decision status=%d body=%s",
					response.Code,
					response.Body.String(),
				)
			}

			if err != nil {
				t.Fatalf("wait failed: %v", err)
			}

			if actual != decision {
				t.Fatalf(
					"got %q, want %q",
					actual,
					decision,
				)
			}

			for _, approval := range agentApprovalStore.Pending() {
				if approval.ID == item.ID {
					t.Fatal("resolved approval still pending")
				}
			}
		})
	}
}

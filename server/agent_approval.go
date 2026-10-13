package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	agentpkg "github.com/ollama/ollama/internal/agent"
	"github.com/ollama/ollama/internal/muse"
)

var agentApprovalStore = muse.NewApprovalStore()

func configureAgentApprovals(request *agentpkg.RunRequest) {
	request.EnforceApprovals = true

	request.ApproveTool = func(
		ctx context.Context,
		approval agentpkg.ToolApprovalRequest,
	) (bool, error) {
		action := agentpkg.MuseActionForTool(approval.ToolName)

		item := agentApprovalStore.Request(muse.Request{
			SessionID: approval.SessionID,
			Action:    action,
			Target:    approval.Path,
			Args:      approval.Arguments,
		})

		decision, err := agentApprovalStore.Wait(
			ctx,
			item,
			muse.PolicyFor(action),
		)
		if err != nil {
			return false, err
		}

		return decision == muse.DecisionAllow, nil
	}
}

func (s *Server) AgentPendingApprovalsHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"approvals": agentApprovalStore.Pending(),
	})
}

type agentApprovalDecisionRequest struct {
	Decision muse.Decision `json:"decision"`
}

func (s *Server) AgentApprovalDecisionHandler(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		c.AbortWithStatusJSON(
			http.StatusBadRequest,
			gin.H{"error": "approval id is required"},
		)
		return
	}

	var request agentApprovalDecisionRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.AbortWithStatusJSON(
			http.StatusBadRequest,
			gin.H{"error": err.Error()},
		)
		return
	}

	if err := agentApprovalStore.Decide(id, request.Decision); err != nil {
		c.AbortWithStatusJSON(
			http.StatusNotFound,
			gin.H{"error": err.Error()},
		)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":       id,
		"decision": request.Decision,
	})
}

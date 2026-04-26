package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// CopilotOAuthHandler handles GitHub Device Code Flow for Copilot accounts.
type CopilotOAuthHandler struct {
	copilotOAuthService *service.CopilotOAuthService
}

// NewCopilotOAuthHandler creates a new CopilotOAuthHandler.
func NewCopilotOAuthHandler(copilotOAuthService *service.CopilotOAuthService) *CopilotOAuthHandler {
	return &CopilotOAuthHandler{
		copilotOAuthService: copilotOAuthService,
	}
}

// InitiateDeviceCode starts the GitHub Device Code Flow.
// POST /api/v1/admin/copilot/oauth/device-code
func (h *CopilotOAuthHandler) InitiateDeviceCode(c *gin.Context) {
	result, err := h.copilotOAuthService.InitiateDeviceCode(c.Request.Context())
	if err != nil {
		response.InternalError(c, "发起 Device Code Flow 失败: "+err.Error())
		return
	}
	response.Success(c, result)
}

type copilotPollTokenRequest struct {
	DeviceCode string `json:"device_code" binding:"required"`
}

// PollToken polls GitHub for an access token using the device code.
// POST /api/v1/admin/copilot/oauth/poll-token
func (h *CopilotOAuthHandler) PollToken(c *gin.Context) {
	var req copilotPollTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求无效: "+err.Error())
		return
	}

	tokenResp, err := h.copilotOAuthService.PollAccessToken(c.Request.Context(), req.DeviceCode)
	if err != nil {
		response.InternalError(c, "轮询 Token 失败: "+err.Error())
		return
	}

	// Handle polling states — return token status only; account creation is handled by the frontend.
	switch tokenResp.Error {
	case "":
		// Success — return access token for the frontend to create the account
		response.Success(c, gin.H{
			"status":       "success",
			"access_token": tokenResp.AccessToken,
		})

	case "authorization_pending":
		response.Success(c, gin.H{"status": "pending"})

	case "slow_down":
		response.Success(c, gin.H{"status": "slow_down", "interval": tokenResp.Interval})

	default:
		// Terminal OAuth errors (access_denied, incorrect_device_code, etc.)
		response.Success(c, gin.H{"status": "error", "error": tokenResp.Error})
	}
}

// GetDefaultModelMapping returns the default model mapping for Copilot.
// GET /api/v1/admin/copilot/default-model-mapping
func (h *CopilotOAuthHandler) GetDefaultModelMapping(c *gin.Context) {
	response.Success(c, domain.DefaultCopilotModelMapping)
}

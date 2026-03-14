package admin

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// UsageScriptHandler 用量脚本管理 HTTP 处理器
type UsageScriptHandler struct {
	repo service.UsageScriptRepository
}

// NewUsageScriptHandler 创建用量脚本处理器
func NewUsageScriptHandler(repo service.UsageScriptRepository) *UsageScriptHandler {
	return &UsageScriptHandler{repo: repo}
}

// CreateUsageScriptRequest 创建用量脚本请求
type CreateUsageScriptRequest struct {
	BaseURLHost string `json:"base_url_host" binding:"required"`
	AccountType string `json:"account_type" binding:"required"`
	Script      string `json:"script" binding:"required"`
	Enabled     *bool  `json:"enabled"`
}

// UpdateUsageScriptRequest 更新用量脚本请求
type UpdateUsageScriptRequest struct {
	BaseURLHost string `json:"base_url_host" binding:"required"`
	AccountType string `json:"account_type" binding:"required"`
	Script      string `json:"script" binding:"required"`
	Enabled     *bool  `json:"enabled"`
}

// List 获取所有用量脚本
// GET /api/v1/admin/usage-scripts
func (h *UsageScriptHandler) List(c *gin.Context) {
	scripts, err := h.repo.List(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, scripts)
}

// Create 创建用量脚本
// POST /api/v1/admin/usage-scripts
func (h *UsageScriptHandler) Create(c *gin.Context) {
	var req CreateUsageScriptRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	script := &service.UsageScript{
		BaseURLHost: req.BaseURLHost,
		AccountType: req.AccountType,
		Script:      req.Script,
		Enabled:     true,
	}
	if req.Enabled != nil {
		script.Enabled = *req.Enabled
	}

	created, err := h.repo.Create(c.Request.Context(), script)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, created)
}

// Update 更新用量脚本
// PUT /api/v1/admin/usage-scripts/:id
func (h *UsageScriptHandler) Update(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid script ID")
		return
	}

	var req UpdateUsageScriptRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	script := &service.UsageScript{
		BaseURLHost: req.BaseURLHost,
		AccountType: req.AccountType,
		Script:      req.Script,
		Enabled:     true,
	}
	if req.Enabled != nil {
		script.Enabled = *req.Enabled
	}

	updated, err := h.repo.Update(c.Request.Context(), id, script)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, updated)
}

// Delete 删除用量脚本
// DELETE /api/v1/admin/usage-scripts/:id
func (h *UsageScriptHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid script ID")
		return
	}

	if err := h.repo.Delete(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "Usage script deleted successfully"})
}

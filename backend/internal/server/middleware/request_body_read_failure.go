package middleware

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// AbortWithRequestBodyReadFailure writes the shared response for a failed body
// read in middleware that inspects the body before the handler. It applies the
// same classification and payload-free diagnostics as the gateway handlers.
func AbortWithRequestBodyReadFailure(c *gin.Context, component string, err error) {
	failure := httputil.DescribeRequestBodyReadFailure(c.Request, err)
	if failure.Kind != httputil.RequestBodyReadErrorMaxBytes {
		logRequestBodyReadFailure(c, component, failure)
	}
	errType := "invalid_request_error"
	body := gin.H{}
	if c.Request.URL.Path == "/v1/files" && service.IsDeepseekFilesAnthropicRequest(c) {
		body["type"] = "error"
		if failure.Status == http.StatusRequestEntityTooLarge {
			errType = "request_too_large"
		}
	}
	body["error"] = gin.H{"type": errType, "message": failure.Message}
	c.AbortWithStatusJSON(failure.Status, body)
}

func logRequestBodyReadFailure(c *gin.Context, component string, failure httputil.RequestBodyReadFailure) {
	fields := append([]zap.Field{zap.String("component", component)}, failure.LogFields()...)
	logger.FromContext(c.Request.Context()).Warn("read request body failed", fields...)
}

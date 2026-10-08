package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Cross-provider fallback must be able to distinguish an upstream 429 from a
// local user/group quota denial, even for the Messages protocol's error mapper.
func TestAnthropicFailoverExhaustedPreservesUpstreamSignal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	h := &OpenAIGatewayHandler{}
	h.handleAnthropicFailoverExhausted(c, &service.UpstreamFailoverError{StatusCode: 429, ResponseBody: []byte(`{"error":{"message":"quota exhausted"}}`)}, false)
	require.Equal(t, 429, recorder.Code)
	require.Equal(t, 429, c.GetInt(service.OpsUpstreamStatusCodeKey))
	require.Equal(t, "quota exhausted", c.GetString(service.OpsUpstreamErrorMessageKey))
}

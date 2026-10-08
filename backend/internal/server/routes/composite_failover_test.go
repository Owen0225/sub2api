package routes

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCompositeFailoverAcrossProviders(t *testing.T) {
	for _, tc := range []struct {
		name      string
		first     int
		second    int
		stream    bool
		want      int
		platforms []string
	}{
		{"empty primary", 503, 200, false, 200, []string{"zhipu", "opencode_go"}},
		{"empty primary sanitized", 503, 200, false, 200, []string{"zhipu", "opencode_go"}},
		{"quota exhausted", 429, 200, false, 200, []string{"zhipu", "opencode_go"}},
		{"both exhausted use ollama", 429, 503, false, 200, []string{"zhipu", "opencode_go", "anthropic"}},
		{"authentication is terminal", 401, 200, false, 401, []string{"zhipu"}},
		{"bad request is terminal", 400, 200, false, 400, []string{"zhipu"}},
		{"already streaming", 200, 200, true, 200, []string{"zhipu"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			resolver := service.NewCompositeRouteResolver(nil)
			resolver.SetModelOwnershipResolver(func(context.Context, int64, string) (service.CompositeModelOwnership, error) {
				return service.CompositeModelOwnership{Ambiguous: true, Platforms: []string{"anthropic", "zhipu", "opencode_go"}}, nil
			})
			r := gin.New()
			r.Use(func(c *gin.Context) {
				id := int64(7)
				c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{GroupID: &id, Group: &service.Group{ID: id, Platform: service.PlatformComposite}})
			})
			r.Use(compositeTargetPlatformMiddleware(resolver))
			var called []string
			r.POST("/v1/chat/completions", compositeFailoverHandler(func(c *gin.Context) {
				platform, _ := service.ResolvedTargetPlatformFromContext(c.Request.Context())
				called = append(called, platform)
				body, err := io.ReadAll(c.Request.Body)
				require.NoError(t, err)
				require.JSONEq(t, `{"model":"glm-5.3","messages":[{"role":"user","content":"hello"}]}`, string(body))
				require.False(t, service.IsResponseCommitted(c), "previous attempt must not commit the next response")
				if tc.stream {
					c.Writer.WriteHeader(200)
					_, _ = c.Writer.WriteString("data: first\n\n")
					c.Writer.Flush()
					service.SetOpsUpstreamError(c, 503, "late failure", "")
					return
				}
				status := 200
				if len(called) == 1 {
					status = tc.first
				} else if len(called) == 2 {
					status = tc.second
				}
				if status != 200 {
					c.Header("Retry-After", "60")
					if tc.name == "empty primary sanitized" {
						c.Set(service.OpsRoutingCapacityLimitedKey, true)
						c.JSON(status, gin.H{"error": gin.H{"type": "api_error", "message": "Service temporarily unavailable"}})
						return
					}
					service.SetOpsUpstreamError(c, status, "upstream exhausted", "")
					service.MarkResponseCommitted(c)
					c.JSON(status, gin.H{"error": gin.H{"type": "api_error", "message": "No available accounts"}})
					return
				}
				require.Empty(t, c.Writer.Header().Get("Retry-After"))
				c.JSON(200, gin.H{"provider": platform})
			}))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"glm-5.3","messages":[{"role":"user","content":"hello"}]}`)))
			require.Equal(t, tc.want, w.Code)
			require.Equal(t, tc.platforms, called)
			if tc.want == 200 && !tc.stream {
				require.NotContains(t, w.Body.String(), "error")
				require.Contains(t, w.Body.String(), tc.platforms[len(tc.platforms)-1])
			}
		})
	}
}

func TestCompositeFailoverDoesNotBypassLocalDenialsOrReplayFlushedErrors(t *testing.T) {
	for _, mode := range []string{"user_rate_limit", "policy_denied", "flushed_error", "large_error", "canceled", "all_exhausted"} {
		t.Run(mode, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			r := gin.New()
			calls := 0
			r.POST("/", func(c *gin.Context) {
				c.Set(compositeCandidatesKey, []service.CompositeRouteDecision{
					{Matched: true, TargetPlatform: service.PlatformZhipu, UpstreamModel: "glm-5.3"},
					{Matched: true, TargetPlatform: service.PlatformOpenCodeGo, UpstreamModel: "glm-5.3"},
				})
			}, compositeFailoverHandler(func(c *gin.Context) {
				calls++
				if mode == "user_rate_limit" {
					c.JSON(429, gin.H{"error": gin.H{"message": "user concurrency exceeded"}})
					return
				}
				if mode == "policy_denied" {
					service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalPolicyDenied)
				}
				service.SetOpsUpstreamError(c, 503, "exhausted", "")
				c.Header("Retry-After", "30")
				if mode == "large_error" {
					c.String(503, strings.Repeat("x", 65*1024))
					return
				}
				c.AbortWithStatusJSON(503, gin.H{"error": gin.H{"message": "No available accounts"}})
				if mode == "flushed_error" {
					c.Writer.Flush()
				}
			}))
			req := httptest.NewRequest("POST", "/", strings.NewReader(`{"model":"glm-5.3"}`))
			if mode == "canceled" {
				ctx, cancel := context.WithCancel(req.Context())
				cancel()
				req = req.WithContext(ctx)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			expectedCalls := 1
			if mode == "all_exhausted" {
				expectedCalls = 2
			}
			require.Equal(t, expectedCalls, calls)
			if mode == "user_rate_limit" {
				require.Equal(t, 429, w.Code)
			} else {
				require.Equal(t, 503, w.Code)
				require.Equal(t, "30", w.Header().Get("Retry-After"))
			}
			if mode == "all_exhausted" {
				require.JSONEq(t, `{"error":{"message":"No available accounts"}}`, w.Body.String())
			}
		})
	}
}

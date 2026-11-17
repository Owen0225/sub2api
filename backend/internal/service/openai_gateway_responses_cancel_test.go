//go:build unit

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// ccDisconnectTestUpstream 模拟 CC 上游的两种断开语义：
//   - 上游请求 ctx 挂载了客户端（cancelCCUpstreamWithClientKey 标记）时，
//     客户端断开 ⇒ 上游读流立刻报错（等价真实 http.Client 在 ctx 取消时的行为）；
//   - 上游 ctx 与客户端解绑时，上游自行把流走完（usage + [DONE]），
//     模拟断开排水语义。
type ccDisconnectTestUpstream struct {
	mu      sync.Mutex
	request *http.Request
}

func (u *ccDisconnectTestUpstream) record(req *http.Request) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.request = req
}

func (u *ccDisconnectTestUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.record(req)
	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write([]byte(ccDisconnectTestChunk("a")))
		_, _ = pw.Write([]byte(ccDisconnectTestChunk("b")))
		if req.Context().Value(cancelCCUpstreamWithClientKey{}) == true {
			<-req.Context().Done()
			_ = pw.CloseWithError(req.Context().Err())
			return
		}
		_, _ = pw.Write([]byte(ccDisconnectTestUsageChunk()))
		_, _ = pw.Write([]byte("data: [DONE]\n\n"))
		_ = pw.Close()
	}()
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       pr,
	}, nil
}

func (u *ccDisconnectTestUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, concurrency)
}

func ccDisconnectTestChunk(content string) string {
	return fmt.Sprintf("data: {\"id\":\"chatcmpl_cancel\",\"choices\":[{\"index\":0,\"delta\":{\"content\":%q}}]}\n\n", content)
}

func ccDisconnectTestUsageChunk() string {
	return "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":7,\"total_tokens\":10}}\n\n"
}

func ccDisconnectTestContext(t *testing.T) (*gin.Context, context.CancelFunc) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	clientCtx, cancel := context.WithCancel(context.Background())
	c.Request = c.Request.WithContext(clientCtx)
	return c, cancel
}

// 默认（未设置 extra 标记）也必须把上游请求挂到客户端 ctx 上。
func TestCancelCCUpstreamOnClientDisconnectDefaultsToEnabled(t *testing.T) {
	require.True(t, cancelCCUpstreamOnClientDisconnect(nil))
	require.True(t, cancelCCUpstreamOnClientDisconnect(&Account{}))
	require.True(t, cancelCCUpstreamOnClientDisconnect(&Account{Extra: map[string]any{}}))
	require.False(t, cancelCCUpstreamOnClientDisconnect(&Account{Extra: map[string]any{
		"cancel_upstream_on_client_disconnect": false,
	}}))
	require.True(t, cancelCCUpstreamOnClientDisconnect(&Account{Extra: map[string]any{
		"cancel_upstream_on_client_disconnect": true,
	}}))
}

// responses→CC 回退路径：客户端断开后必须取消上游，并按零 token 记账。
func TestForwardResponses_ChatFallbackCancelsUpstreamOnClientDisconnect(t *testing.T) {
	upstream := &ccDisconnectTestUpstream{}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
	account := forceChatResponsesFallbackAccount() // 无 cancel 标记 ⇒ 默认开启

	body := []byte(`{"model":"gpt-5.4","input":"hello","stream":true}`)
	c, cancelClient := ccDisconnectTestContext(t)

	type forwardResult struct {
		result *OpenAIForwardResult
		err    error
	}
	done := make(chan forwardResult, 1)
	go func() {
		result, err := svc.forwardResponsesViaRawChatCompletions(context.Background(), c, account, body, time.Now())
		done <- forwardResult{result, err}
	}()

	// 等上游请求发出，然后模拟客户端走人。
	deadline := time.Now().Add(5 * time.Second)
	for {
		upstream.mu.Lock()
		sent := upstream.request != nil
		upstream.mu.Unlock()
		if sent || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.NotNil(t, upstream.request, "upstream request should have been sent")
	cancelClient()

	select {
	case got := <-done:
		require.Error(t, got.err, "canceled upstream read should surface stream usage incomplete")
		require.NotNil(t, got.result)
		require.True(t, got.result.ClientDisconnect)
		require.Equal(t, OpenAIUsage{}, got.result.Usage)
	case <-time.After(5 * time.Second):
		t.Fatal("forward did not return after client disconnect")
	}
}

// 显式 opt-out：恢复旧的断开排水语义，上游走完后全额入账。
func TestForwardResponses_ChatFallbackOptOutKeepsDrainingForBilling(t *testing.T) {
	upstream := &ccDisconnectTestUpstream{}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
	account := forceChatResponsesFallbackAccount()
	account.Extra["cancel_upstream_on_client_disconnect"] = false

	body := []byte(`{"model":"gpt-5.4","input":"hello","stream":true}`)
	c, cancelClient := ccDisconnectTestContext(t)

	type forwardResult struct {
		result *OpenAIForwardResult
		err    error
	}
	done := make(chan forwardResult, 1)
	go func() {
		result, err := svc.forwardResponsesViaRawChatCompletions(context.Background(), c, account, body, time.Now())
		done <- forwardResult{result, err}
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		upstream.mu.Lock()
		sent := upstream.request != nil
		upstream.mu.Unlock()
		if sent || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.NotNil(t, upstream.request, "upstream request should have been sent")
	cancelClient()

	select {
	case got := <-done:
		require.NoError(t, got.err)
		require.NotNil(t, got.result)
		require.False(t, got.result.ClientDisconnect)
		require.Equal(t, 3, got.result.Usage.InputTokens)
		require.Equal(t, 7, got.result.Usage.OutputTokens)
	case <-time.After(5 * time.Second):
		t.Fatal("forward did not return after drain completed")
	}
}

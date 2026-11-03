package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMessagesCCCancellationUsesClientContextOnlyWhenEnabled(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "default_detached", true: "opt_in_attached"}[enabled], func(t *testing.T) {
			upstream := &openCodeSessionHTTPUpstream{}
			svc := openCodeSessionTestService()
			svc.httpUpstream = upstream
			account := openCodeSessionTestAccount("https://example.com/v1")
			account.Extra = map[string]any{"cancel_upstream_on_client_disconnect": enabled}
			c := newOpenCodeSessionTestContext(t, "")
			clientCtx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := clientCtx
			if cancelCCUpstreamOnClientDisconnect(account) {
				ctx = context.WithValue(ctx, cancelCCUpstreamWithClientKey{}, true)
			}
			resp, err := svc.sendCCUpstreamRequest(ctx, c, account, "https://example.com/v1/chat/completions", []byte(`{}`), true, "token", "", "")
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			require.Equal(t, http.MethodPost, upstream.request.Method)
			cancel()
			select {
			case <-upstream.request.Context().Done():
				require.True(t, enabled)
			default:
				require.False(t, enabled)
			}
		})
	}
}

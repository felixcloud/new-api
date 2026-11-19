package claude

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type blockingReadCloser struct {
	ctx context.Context
}

func (r *blockingReadCloser) Read(_ []byte) (int, error) {
	<-r.ctx.Done()
	return 0, r.ctx.Err()
}

func (r *blockingReadCloser) Close() error { return nil }

func TestClaudeStreamHandlerSkipsEstimateWhenNoEventsReceived(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	tests := []struct {
		name           string
		cancelBefore   bool
		body           string
		wantPrompt     int
		wantCompletion bool
		wantLocalCount bool
	}{
		{
			name:           "client_gone with zero received events bills zero",
			cancelBefore:   true,
			wantPrompt:     0,
			wantCompletion: false,
			wantLocalCount: false,
		},
		{
			name:           "empty eof with zero received events bills zero",
			body:           "",
			wantPrompt:     0,
			wantCompletion: false,
			wantLocalCount: false,
		},
		{
			name:           "partial stream after receiving delta still estimates usage",
			body:           "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello world\"}}\n\n",
			wantPrompt:     523843,
			wantCompletion: true,
			wantLocalCount: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			reqCtx, cancel := context.WithCancel(context.Background())
			defer cancel()

			req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(reqCtx)
			c.Request = req

			info := &relaycommon.RelayInfo{
				RelayFormat:  types.RelayFormatClaude,
				IsStream:     true,
				StreamStatus: relaycommon.NewStreamStatus(),
				ChannelMeta: &relaycommon.ChannelMeta{
					UpstreamModelName: "claude-fable-5-2",
				},
			}
			info.SetEstimatePromptTokens(523843)

			var body io.ReadCloser
			if tc.cancelBefore {
				cancel()
				body = &blockingReadCloser{ctx: reqCtx}
			} else {
				body = io.NopCloser(strings.NewReader(tc.body))
			}

			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       body,
			}

			usage, apiErr := ClaudeStreamHandler(c, resp, info)
			require.Nil(t, apiErr)
			require.NotNil(t, usage)

			assert.Equal(t, tc.wantPrompt, usage.PromptTokens)
			if tc.wantCompletion {
				assert.Greater(t, usage.CompletionTokens, 0)
			} else {
				assert.Zero(t, usage.CompletionTokens)
				assert.Zero(t, usage.TotalTokens)
				assert.Nil(t, usage.BillingUsage)
			}
			assert.Equal(t, tc.wantLocalCount, common.GetContextKeyBool(c, constant.ContextKeyLocalCountTokens))
		})
	}
}

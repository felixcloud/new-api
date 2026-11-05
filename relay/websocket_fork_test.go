package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

type trackCloseReadCloser struct {
	io.Reader
	closed bool
}

func (t *trackCloseReadCloser) Close() error {
	t.closed = true
	return nil
}

func TestForkResolveTargetWs_NonWebsocketResponse(t *testing.T) {
	body := &trackCloseReadCloser{Reader: strings.NewReader("bad request")}
	httpResp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Body:       body,
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType: constant.ChannelTypeAli,
		},
	}

	wsConn, apiErr := forkResolveTargetWs(httpResp, info)
	if wsConn != nil {
		t.Fatalf("expected nil wsConn, got %v", wsConn)
	}
	if apiErr == nil {
		t.Fatal("expected NewAPIError, got nil")
	}
	if apiErr.GetErrorCode() != types.ErrorCodeInvalidApiType {
		t.Fatalf("expected error code %s, got %s", types.ErrorCodeInvalidApiType, apiErr.GetErrorCode())
	}
	if !body.closed {
		t.Fatal("expected http.Response.Body to be closed")
	}
}

func TestWssHelper_AliChannelNilRequestBodyNoPanic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"unsupported"}`))
	}))
	defer upstream.Close()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req, err := http.NewRequest(http.MethodGet, "/v1/realtime?model=qwen3-asr-flash-realtime-2026-02-10", nil)
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	c.Request = req
	c.Set("channel_type", constant.ChannelTypeAli)
	c.Set("channel_id", 104)
	c.Set("base_url", upstream.URL)

	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeRealtime,
		OriginModelName: "qwen3-asr-flash-realtime-2026-02-10",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeAli,
			ChannelId:         104,
			ChannelBaseUrl:    upstream.URL,
			ApiType:           constant.ChannelTypeAli,
			UpstreamModelName: "qwen3-asr-flash-realtime-2026-02-10",
		},
	}

	apiErr := WssHelper(c, info)
	if apiErr == nil {
		t.Fatal("expected WssHelper to return an error for ali channel, got nil")
	}
	if apiErr.GetErrorCode() != types.ErrorCodeInvalidApiType {
		t.Fatalf("expected error code %s, got %s (%v)", types.ErrorCodeInvalidApiType, apiErr.GetErrorCode(), apiErr)
	}
}

package relay

import (
	"fmt"
	"net/http"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gorilla/websocket"
)

// forkResolveTargetWs Safely extracts *websocket.Conn from adaptor.DoRequest result.
// When a Realtime request is routed to a channel whose adaptor returns *http.Response
// instead of *websocket.Conn (e.g. ali channel type=17), close the HTTP body and return
// a structured NewAPIError instead of panicking on type assertion.
func forkResolveTargetWs(resp any, info *relaycommon.RelayInfo) (*websocket.Conn, *types.NewAPIError) {
	wsConn, ok := resp.(*websocket.Conn)
	if ok {
		return wsConn, nil
	}
	if httpResp, ok := resp.(*http.Response); ok && httpResp != nil && httpResp.Body != nil {
		_ = httpResp.Body.Close()
	}
	return nil, types.NewError(
		fmt.Errorf("channel type %d does not support realtime websocket", info.ChannelType),
		types.ErrorCodeInvalidApiType,
		types.ErrOptionWithSkipRetry(),
	)
}

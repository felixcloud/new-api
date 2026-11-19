package openai

import relaycommon "github.com/QuantumNous/new-api/relay/common"

// forkShouldSkipEmptyStreamEstimate skips local token-estimation fallback when
// StreamScannerHandler ran and received zero SSE chunks from upstream (e.g.
// upstream hung until client_gone / timeout / empty EOF).
func forkShouldSkipEmptyStreamEstimate(info *relaycommon.RelayInfo, responseTextLen, toolCount int) bool {
	if info == nil || info.StreamStatus == nil || info.ReceivedResponseCount > 0 {
		return false
	}
	return responseTextLen == 0 && toolCount == 0
}

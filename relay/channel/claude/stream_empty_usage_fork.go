package claude

import relaycommon "github.com/QuantumNous/new-api/relay/common"

// forkShouldSkipEmptyStreamEstimate skips local token-estimation fallback when
// StreamScannerHandler ran and received zero SSE events from upstream (e.g.
// upstream hung until client_gone / timeout / empty EOF). Without this guard,
// HandleStreamFinalResponse fabricates PromptTokens from GetEstimatePromptTokens
// and bills the customer for an unserved request.
func forkShouldSkipEmptyStreamEstimate(info *relaycommon.RelayInfo, claudeInfo *ClaudeResponseInfo) bool {
	if info == nil || info.StreamStatus == nil || info.ReceivedResponseCount > 0 {
		return false
	}
	if claudeInfo != nil && (claudeInfo.Done || claudeInfo.ResponseText.Len() > 0 || claudeInfo.ToolUseCount > 0) {
		return false
	}
	return true
}

package plugins_test

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSoraResponsesProtocol(t *testing.T) {
	testVideoResponsesProtocol(t, videoResponsesTestCase{
		pluginKey: "sora",
		model:     "sora-2-pro",
		requestBody: map[string]any{
			"model":   "sora-2-pro",
			"input":   "waves at sunset",
			"seconds": 8,
			"size":    "1792x1024",
		},
		wantAction: "text_to_video",
		wantRequest: map[string]any{
			"model":   "sora-2-pro",
			"prompt":  "waves at sunset",
			"seconds": float64(8),
			"size":    "1792x1024",
		},
		wantUsageKeys:  []string{"seconds", "size"},
		wantVendorName: "sora",
	})
}

// yunai fork: compatible gateways serving Seedance via /v1/videos return
// metadata.url without usage on /v1/videos/{id}, while /v1/video/generations/{id}
// carries Volcengine usage.completion_tokens, resolution, duration, and generate_audio.
func TestSoraCompatibleGatewaySeedanceUsage(t *testing.T) {
	source, err := builtinplugins.Source("sora")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: "sora"})
	require.NoError(t, err)

	callHook := func(t *testing.T, ctx context.Context, hook string, args ...any) map[string]any {
		t.Helper()
		value, err := plugin.Engine.Call(ctx, hook, args...)
		require.NoError(t, err)
		encoded, err := common.Marshal(value)
		require.NoError(t, err)
		var out map[string]any
		require.NoError(t, common.Unmarshal(encoded, &out))
		return out
	}

	t.Run("extractUsage includes resolution and video_input for Seedance on OpenAI-compatible channels", func(t *testing.T) {
		facts := callHook(t, t.Context(), "extractUsage", map[string]any{
			"model":         "doubao-seedance-2-5-260628",
			"upstreamModel": "doubao-seedance-2-5-260628",
			"usagePurpose":  "facts",
			"requestBody": map[string]any{
				"model":   "doubao-seedance-2-5-260628",
				"seconds": 8,
				"size":    "1280x720",
				"metadata": map[string]any{
					"resolution": "720p",
					"content": []any{
						map[string]any{"type": "video_url", "video_url": map[string]any{"url": "https://cdn.example/ref.mp4"}},
					},
				},
			},
		})
		assert.Equal(t, map[string]any{
			"seconds":     float64(8),
			"size":        "1280x720",
			"resolution":  "720p",
			"video_input": "video",
		}, facts)
	})

	t.Run("parseTaskResult and extractUsageOnComplete enrich usage from /v1/video/generations/{id}", func(t *testing.T) {
		const (
			baseURL = "https://gateway.example"
			taskID  = "cgt-20261105175336-9lwfd"
		)
		var gotReq jsplugin.FetchRequest
		ctx := jsplugin.WithFetcher(t.Context(), func(_ context.Context, req jsplugin.FetchRequest) (jsplugin.FetchResponse, error) {
			gotReq = req
			return jsplugin.FetchResponse{
				Status:  200,
				Headers: map[string]string{"Content-Type": "application/json"},
				Body: []byte(`{
					"id": "cgt-20261105175336-9lwfd",
					"model": "doubao-seedance-2-0-260128",
					"status": "succeeded",
					"content": {"video_url": "https://cdn.example/out.mp4"},
					"usage": {"completion_tokens": 236880, "total_tokens": 236880},
					"resolution": "720p",
					"ratio": "16:9",
					"duration": 11,
					"generate_audio": true
				}`),
			}, nil
		})

		queryCtx := map[string]any{
			"taskId":        taskID,
			"baseUrl":       baseURL,
			"apiKey":        "sk-byteboard-test",
			"model":         "doubao-seedance-2-5-260628",
			"upstreamModel": "doubao-seedance-2-0-260128",
		}
		pollBody := map[string]any{
			"id":       taskID,
			"object":   "video",
			"status":   "completed",
			"progress": 100,
			"model":    "doubao-seedance-2-0-260128",
			"seconds":  "11",
			"size":     "16:9",
			"metadata": map[string]any{
				"url": "https://cdn.example/out.mp4",
			},
		}

		parsed := callHook(t, ctx, "parseTaskResult", queryCtx, pollBody)
		assert.Equal(t, baseURL+"/v1/video/generations/"+taskID, gotReq.URL)
		assert.Equal(t, "Bearer sk-byteboard-test", gotReq.Headers["Authorization"])
		assert.Equal(t, "SUCCESS", parsed["status"])
		assert.Equal(t, "https://cdn.example/out.mp4", parsed["url"])
		assert.EqualValues(t, 236880, parsed["totalTokens"])

		taskResult := map[string]any{
			"status":            parsed["status"],
			"url":               parsed["url"],
			"completion_tokens": parsed["completionTokens"],
			"total_tokens":      parsed["totalTokens"],
			"plugin_state":      parsed["state"],
		}
		settled := callHook(t, t.Context(), "extractUsageOnComplete", queryCtx, taskResult, pollBody)
		assert.Equal(t, map[string]any{
			"seconds":        float64(11),
			"tokens":         float64(236880),
			"resolution":     "720p",
			"video_input":    "none",
			"generate_audio": true,
		}, settled)
	})
}

package plugins_test

// yunai fork: the kling-omni task plugin (Kling 3.0 Omni and Kling O1 on /v1/videos/omni-video).

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKlingOmniResponsesProtocol(t *testing.T) {
	testVideoResponsesProtocol(t, videoResponsesTestCase{
		pluginKey: "kling-omni",
		model:     "kling-v3-omni",
		requestBody: map[string]any{
			"model": "kling-v3-omni",
			"input": []any{map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "input_text", "text": "camera orbit"},
				map[string]any{"type": "input_image", "image_url": "https://cdn.example/frame.png"},
			}}},
			"seconds":  10,
			"metadata": map[string]any{"mode": "pro", "sound": "on"},
		},
		wantAction: "omni_video",
		wantRequest: map[string]any{
			"model": "kling-v3-omni",
			"vendor": map[string]any{
				"prompt":     "camera orbit",
				"image_list": []any{map[string]any{"image_url": "https://cdn.example/frame.png", "type": "first_frame"}},
				"duration":   "10",
				"mode":       "pro",
				"sound":      "on",
			},
		},
		wantUsageKeys:  []string{"units"},
		wantVendorName: "kling",
	})
}

func loadKlingOmni(t *testing.T) *jsplugin.LoadedPlugin {
	t.Helper()
	source, err := builtinplugins.Source("kling-omni")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: "kling-omni"})
	require.NoError(t, err)
	return plugin
}

func callKlingOmni(t *testing.T, plugin *jsplugin.LoadedPlugin, hook string, args ...any) (map[string]any, error) {
	t.Helper()
	value, err := plugin.Engine.Call(t.Context(), hook, args...)
	if err != nil {
		return nil, err
	}
	encoded, err := common.Marshal(value)
	require.NoError(t, err)
	var result map[string]any
	require.NoError(t, common.Unmarshal(encoded, &result))
	return result, nil
}

func klingOmniDriver(model string, vendor map[string]any) map[string]any {
	return map[string]any{
		"action": "omni_video", "model": model, "upstreamModel": model,
		"baseUrl": "https://api-beijing.klingai.com", "apiKey": "api-key-kling-test", "authHeader": "api-key-kling-test",
		"upstream": map[string]any{"kind": "vendor"}, "publicTaskId": "task_pub", "requestHeaders": map[string]any{}, "files": []any{},
		"requestBody": map[string]any{"model": model, "vendor": vendor},
	}
}

func TestKlingOmniSubmitRequest(t *testing.T) {
	plugin := loadKlingOmni(t)

	t.Run("sends a single API key as Bearer and fills billed defaults", func(t *testing.T) {
		submit, err := callKlingOmni(t, plugin, "buildSubmitRequest", klingOmniDriver("kling-v3-omni", map[string]any{"prompt": "a cat"}))
		require.NoError(t, err)
		assert.Equal(t, "https://api-beijing.klingai.com/v1/videos/omni-video", submit["url"])
		assert.Equal(t, "Bearer api-key-kling-test", submit["headers"].(map[string]any)["Authorization"])
		assert.Equal(t, map[string]any{
			"model_name": "kling-v3-omni", "prompt": "a cat", "duration": "5", "mode": "std", "sound": "off", "aspect_ratio": "16:9",
		}, submit["body"])
	})

	t.Run("signs an accessKey|secretKey pair into a JWT", func(t *testing.T) {
		ctx := klingOmniDriver("kling-video-o1", map[string]any{"prompt": "a cat"})
		ctx["apiKey"] = "ak|sk"
		submit, err := callKlingOmni(t, plugin, "buildSubmitRequest", ctx)
		require.NoError(t, err)
		authorization := submit["headers"].(map[string]any)["Authorization"].(string)
		assert.True(t, strings.HasPrefix(authorization, "Bearer ey"), authorization)
	})

	t.Run("addresses a New API gateway on the plugin's native prefix", func(t *testing.T) {
		ctx := klingOmniDriver("kling-v3-omni", map[string]any{"prompt": "a cat"})
		ctx["upstream"] = map[string]any{"kind": "new_api"}
		ctx["apiKey"] = "sk-gateway"
		submit, err := callKlingOmni(t, plugin, "buildSubmitRequest", ctx)
		require.NoError(t, err)
		assert.Equal(t, "https://api-beijing.klingai.com/kling-omni/v1/videos/omni-video", submit["url"])
		assert.Equal(t, "Bearer sk-gateway", submit["headers"].(map[string]any)["Authorization"])

		query, err := callKlingOmni(t, plugin, "buildQueryRequest", map[string]any{"taskId": "tid", "baseUrl": "https://api-beijing.klingai.com", "apiKey": "sk-gateway", "upstream": map[string]any{"kind": "new_api"}})
		require.NoError(t, err)
		assert.Equal(t, "https://api-beijing.klingai.com/kling-omni/v1/videos/omni-video/tid", query["url"])
	})

	t.Run("keeps a first frame or base video in charge of the aspect ratio", func(t *testing.T) {
		submit, err := callKlingOmni(t, plugin, "buildSubmitRequest", klingOmniDriver("kling-video-o1", map[string]any{
			"prompt":     "edit it",
			"video_list": []any{map[string]any{"video_url": "https://cdn.example/in.mp4", "refer_type": "base", "keep_original_sound": "yes"}},
		}))
		require.NoError(t, err)
		_, hasAspect := submit["body"].(map[string]any)["aspect_ratio"]
		assert.False(t, hasAspect)
	})
}

// Reserved units follow the official per-second unit table; settlement uses final_unit_deduction.
func TestKlingOmniEstimatesUnits(t *testing.T) {
	plugin := loadKlingOmni(t)
	video := []any{map[string]any{"video_url": "https://cdn.example/in.mp4", "refer_type": "feature"}}
	cases := []struct {
		name   string
		model  string
		vendor map[string]any
		units  float64
	}{
		{"v3-omni std 5s", "kling-v3-omni", map[string]any{"prompt": "x"}, 3},
		{"v3-omni pro with audio 5s", "kling-v3-omni", map[string]any{"prompt": "x", "mode": "pro", "sound": "on"}, 5},
		{"v3-omni pro with video input 10s", "kling-v3-omni", map[string]any{"prompt": "x", "mode": "pro", "duration": "10", "video_list": video}, 12},
		{"v3-omni 4k 15s", "kling-v3-omni", map[string]any{"prompt": "x", "mode": "4k", "duration": 15}, 45},
		{"o1 std 5s", "kling-video-o1", map[string]any{"prompt": "x"}, 3},
		{"o1 std with reference image 3s", "kling-video-o1", map[string]any{"prompt": "x", "duration": "3", "image_list": []any{map[string]any{"image_url": "https://cdn.example/a.png", "type": "refer"}}}, 1.8},
		{"o1 std with video input 10s", "kling-video-o1", map[string]any{"prompt": "x", "duration": "10", "video_list": video}, 9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			facts, err := callKlingOmni(t, plugin, "extractUsage", klingOmniDriver(tc.model, tc.vendor))
			require.NoError(t, err)
			assert.Equal(t, tc.units, facts["units"])
		})
	}
}

func TestKlingOmniRejectsOutOfRangeRequests(t *testing.T) {
	plugin := loadKlingOmni(t)
	cases := []struct {
		name    string
		model   string
		vendor  map[string]any
		wantErr string
	}{
		{"o1 duration above 10s", "kling-video-o1", map[string]any{"prompt": "x", "duration": "15"}, "duration must be an integer from 3 to 10"},
		{"o1 text-only 3s", "kling-video-o1", map[string]any{"prompt": "x", "duration": "3"}, "duration must be 5 or 10 seconds for kling-video-o1"},
		{"o1 first frame only 3s", "kling-video-o1", map[string]any{"prompt": "x", "duration": "3", "image_list": []any{map[string]any{"image_url": "https://cdn.example/a.png", "type": "first_frame"}}}, "duration must be 5 or 10 seconds for kling-video-o1"},
		{"v3-omni duration above 15s", "kling-v3-omni", map[string]any{"prompt": "x", "duration": 3600}, "duration must be an integer from 3 to 15"},
		{"fractional duration", "kling-v3-omni", map[string]any{"prompt": "x", "duration": 5.5}, "duration must be an integer"},
		{"o1 has no 4k mode", "kling-video-o1", map[string]any{"prompt": "x", "mode": "4k"}, "mode must be one of std, pro, master for kling-video-o1"},
		{"unknown sound value", "kling-v3-omni", map[string]any{"prompt": "x", "sound": "loud"}, "sound must be on or off"},
		{"missing prompt", "kling-v3-omni", map[string]any{}, "prompt is required"},
		{"unknown model", "kling-v1", map[string]any{"prompt": "x"}, "model must be one of kling-v3-omni, kling-video-o1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := callKlingOmni(t, plugin, "extractUsage", klingOmniDriver(tc.model, tc.vendor))
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

// The task body mirrors a real legacy Kling query response.
func TestKlingOmniParsesTaskResultAndSettlesOnDeductedUnits(t *testing.T) {
	plugin := loadKlingOmni(t)
	body := map[string]any{"code": 0, "message": "SUCCEED", "data": map[string]any{
		"task_id": "938854404139532351", "task_status": "succeed", "task_status_msg": "",
		"task_result":          map[string]any{"videos": []any{map[string]any{"id": "v1", "url": "https://cdn.example/out.mp4", "duration": "5.041"}}},
		"final_unit_deduction": "4.8",
	}}

	result, err := callKlingOmni(t, plugin, "parseTaskResult", map[string]any{}, body)
	require.NoError(t, err)
	assert.Equal(t, "SUCCESS", result["status"])
	assert.Equal(t, "https://cdn.example/out.mp4", result["url"])
	assert.EqualValues(t, 5, result["totalTokens"])

	facts, err := callKlingOmni(t, plugin, "extractUsageOnComplete", map[string]any{}, result, body)
	require.NoError(t, err)
	assert.InDelta(t, 4.8, facts["units"], 1e-9)

	unknown, err := callKlingOmni(t, plugin, "parseTaskResult", map[string]any{}, map[string]any{"data": map[string]any{"task_status": "weird"}})
	require.NoError(t, err)
	assert.Equal(t, "UNKNOWN", unknown["status"])
}

package relay

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestForkImageBillingFacts_ReferenceCounting(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name     string
		req      *dto.ImageRequest
		form     *multipart.Form
		wantRefs int
	}{
		{
			name:     "text to image has zero reference images",
			req:      &dto.ImageRequest{Model: "doubao-seedream-5-5-261028", Prompt: "a cat", Size: "2K"},
			wantRefs: 0,
		},
		{
			name:     "single string image reference",
			req:      &dto.ImageRequest{Model: "doubao-seedream-5-5-261028", Prompt: "edit", Image: json.RawMessage(`"https://example.com/a.png"`)},
			wantRefs: 1,
		},
		{
			name:     "multi image array reference ignores blanks",
			req:      &dto.ImageRequest{Model: "doubao-seedream-5-5-261028", Prompt: "fuse", Image: json.RawMessage(`["https://example.com/a.png","https://example.com/b.png",""]`)},
			wantRefs: 2,
		},
		{
			name:     "images and extra image_urls arrays",
			req:      &dto.ImageRequest{Model: "doubao-seedream-5-5-261028", Prompt: "fuse", Images: json.RawMessage(`[{"url":"https://a"},{"url":"https://b"}]`), Extra: map[string]json.RawMessage{"image_urls": json.RawMessage(`["https://c"]`)}},
			wantRefs: 3,
		},
		{
			name: "multipart uploads count image fields and ignore mask",
			req:  &dto.ImageRequest{Model: "doubao-seedream-5-5-261028", Prompt: "edit"},
			form: &multipart.Form{
				File: map[string][]*multipart.FileHeader{
					"image[]": {{Filename: "a.png"}, {Filename: "b.png"}},
					"mask":    {{Filename: "mask.png"}},
				},
			},
			wantRefs: 2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
			c.Request.MultipartForm = tc.form

			info := &relaycommon.RelayInfo{}
			forkInitImageBillingFacts(c, info, tc.req)
			require.NotNil(t, info.ImageBillingFacts)
			assert.Equal(t, tc.wantRefs, info.ImageBillingFacts.ReferenceImages)
		})
	}
}

func TestForkImageBillingFacts_OpenAINonStreamAndPixelTiers(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name        string
		req         *dto.ImageRequest
		respBody    string
		wantCount   int
		wantRefs    int
		wantStd     int
		wantHd      int
		wantSize    string
		wantQuality string
	}{
		{
			name: "seedream 5.5 layer decomposition mixed pixel tiers with reference image",
			req: &dto.ImageRequest{
				Model: "doubao-seedream-5-5-261028",
				Size:  "2K",
				Image: json.RawMessage(`"https://example.com/src.png"`),
			},
			respBody: `{
				"created": 1783144062,
				"data": [
					{"url": "https://tos.example/bg.png", "size": "2048x2048"},
					{"url": "https://tos.example/layer1.png", "size": "1536x1536"},
					{"url": "https://tos.example/layer2.png", "size": "1024x1024"},
					{"url": "https://tos.example/layer3.png", "size": "2560x1440"}
				],
				"usage": {"generated_images": 4, "output_tokens": 0, "total_tokens": 0}
			}`,
			wantCount: 4,
			wantRefs:  1,
			wantStd:   2,
			wantHd:    2,
			wantSize:  "2K",
		},
		{
			name: "exact 2610000 pixel boundary splits std vs hd",
			req:  &dto.ImageRequest{Model: "doubao-seedream-5-5-261028"},
			respBody: `{
				"data": [
					{"url": "https://tos.example/a.png", "size": "2610x1000"},
					{"url": "https://tos.example/b.png", "size": "2611x1000"}
				]
			}`,
			wantCount: 2,
			wantRefs:  0,
			wantStd:   1,
			wantHd:    1,
		},
		{
			name: "fallback to symbolic request size 1.5K when response omits size",
			req:  &dto.ImageRequest{Model: "doubao-seedream-5-5-261028", Size: "1.5K", Quality: "standard"},
			respBody: `{
				"data": [
					{"url": "https://tos.example/a.png"},
					{"revised_prompt": "ignored without payload"}
				]
			}`,
			wantCount:   1,
			wantRefs:    0,
			wantStd:     1,
			wantHd:      0,
			wantSize:    "1.5K",
			wantQuality: "standard",
		},
		{
			name: "object-shaped data and unknown size defaults conservatively to hd",
			req:  &dto.ImageRequest{Model: "doubao-seedream-5-5-261028", Size: "auto"},
			respBody: `{
				"data": {"b64_json": "iVBORw0KGgo="}
			}`,
			wantCount: 1,
			wantRefs:  0,
			wantStd:   0,
			wantHd:    1,
			wantSize:  "auto",
		},
		{
			name: "split url and b64_json entries bill once per image",
			req:  &dto.ImageRequest{Model: "doubao-seedream-5-5-261028"},
			respBody: `{
				"data": [
					{"url": "https://tos.example/a.png", "size": "1024x1024"},
					{"b64_json": "iVBORw0KGgo=", "size": "1024x1024"}
				]
			}`,
			wantCount: 1,
			wantRefs:  0,
			wantStd:   1,
			wantHd:    0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)

			// ratio 模式: UsePrice=false, TieredBillingSnapshot=nil
			info := &relaycommon.RelayInfo{
				ImageRequestCount: 1,
				ChannelMeta:       &relaycommon.ChannelMeta{},
			}
			forkInitImageBillingFacts(c, info, tc.req)

			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(tc.respBody)),
			}
			_, apiErr := openai.OpenaiImageHandler(c, info, resp)
			require.Nil(t, apiErr)

			other := model.NewLogOther()
			service.ForkAnnotateImageLog(info, other)
			snap := other.Snapshot()

			assert.Equal(t, tc.wantCount, snap["image_count"])
			assert.Equal(t, tc.wantRefs, snap["reference_images"])
			if tc.wantSize != "" {
				assert.Equal(t, tc.wantSize, snap["size"])
			}
			if tc.wantQuality != "" {
				assert.Equal(t, tc.wantQuality, snap["quality"])
			}
			facts, ok := snap["usage_facts"].(map[string]any)
			require.True(t, ok, "usage_facts must be written to other")
			assert.Equal(t, tc.wantCount, facts["image_count"])
			assert.Equal(t, tc.wantRefs, facts["reference_images"])
			assert.Equal(t, tc.wantStd, facts["output_images_std"])
			assert.Equal(t, tc.wantHd, facts["output_images_hd"])
		})
	}
}

func TestForkImageBillingFacts_VolcengineSeedreamStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	sseBody := strings.Join([]string{
		`event: image_generation.partial_succeeded`,
		`data: {"type":"image_generation.partial_succeeded","image_index":0,"url":"https://tos.example/0.png","size":"2048x2048"}`,
		``,
		`event: image_generation.partial_failed`,
		`data: {"type":"image_generation.partial_failed","image_index":1,"error":{"message":"filtered"}}`,
		``,
		`event: image_generation.partial_succeeded`,
		`data: {"type":"image_generation.partial_succeeded","image_index":2,"url":"https://tos.example/2.png","size":"1280x960"}`,
		``,
		`event: image_generation.partial_succeeded`,
		`data: {"type":"image_generation.partial_succeeded","image_index":3,"url":"https://tos.example/3.png","size":"1920x1080"}`,
		``,
		`event: image_generation.completed`,
		`data: {"type":"image_generation.completed","usage":{"generated_images":3,"output_tokens":0,"total_tokens":0}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)

	info := &relaycommon.RelayInfo{
		ImageRequestCount: 1,
		IsStream:          true,
		ChannelMeta:       &relaycommon.ChannelMeta{},
	}
	req := &dto.ImageRequest{
		Model: "doubao-seedream-5-5-261028",
		Image: json.RawMessage(`["https://example.com/ref1.png","https://example.com/ref2.png"]`),
	}
	forkInitImageBillingFacts(c, info, req)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(bytes.NewBufferString(sseBody)),
	}
	_, apiErr := openai.OpenaiImageStreamHandler(c, info, resp)
	require.Nil(t, apiErr)

	other := model.NewLogOther()
	service.ForkAnnotateImageLog(info, other)
	snap := other.Snapshot()

	assert.Equal(t, 3, snap["image_count"])
	assert.Equal(t, 2, snap["reference_images"])
	facts, ok := snap["usage_facts"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, 2, facts["output_images_std"])
	assert.Equal(t, 1, facts["output_images_hd"])
}

func TestForkImageBillingFacts_UpdateImageCountFallbackInRatioMode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)

	info := &relaycommon.RelayInfo{ImageRequestCount: 1}
	forkInitImageBillingFacts(c, info, &dto.ImageRequest{Model: "imagen-4.0-generate-001", Size: "1024x1024"})
	// 模拟非 OpenAI 出图适配器在 ratio 模式下只调用 UpdateImageCount
	info.UpdateImageCount(3)

	other := model.NewLogOther()
	service.ForkAnnotateImageLog(info, other)
	snap := other.Snapshot()

	assert.Equal(t, 3, snap["image_count"])
	assert.Equal(t, 0, snap["reference_images"])
	assert.Equal(t, "1024x1024", snap["size"])
	facts, ok := snap["usage_facts"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, 3, facts["output_images_std"])
	assert.Equal(t, 0, facts["output_images_hd"])
}

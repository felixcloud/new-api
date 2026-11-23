package openai

// yunai fork: 从 OpenAI 兼容 / 火山方舟 Seedream 同步与流式出图响应中采集
// 实际成功输出张数与逐张图片尺寸(data[].size 或 SSE partial_succeeded.size),
// 供日志按像素档位拆分(如 Seedream 5.5 的 <=261 万 / 261-463 万像素分档与图层拆分逐张计价)。

import (
	"strings"

	"github.com/MoeForge/moego/ojson"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// forkCaptureOpenAIImageResponse 解析非流式 / JSON-as-SSE 响应中的图片载荷与逐张尺寸。
// 计数口径严格对齐 openaiImageResponseCount: 对象按是否带 url/b64_json 计 1, 数组取 url 与 b64_json 条数的较大值。
func forkCaptureOpenAIImageResponse(info *relaycommon.RelayInfo, responseBody []byte) {
	if info == nil || info.ImageBillingFacts == nil || len(responseBody) == 0 {
		return
	}
	data := ojson.GetBytesNoCopy(responseBody, "data")
	var sizes []string
	switch {
	case data.IsObject():
		if openaiImageDataHasField(data, "url") || openaiImageDataHasField(data, "b64_json") {
			sizes = []string{forkExtractSizeString(data)}
		}
	case data.IsArray():
		var urlSizes, b64Sizes []string
		data.ForEach(func(_, item ojson.Result) bool {
			sz := forkExtractSizeString(item)
			if openaiImageDataHasField(item, "url") {
				urlSizes = append(urlSizes, sz)
			}
			if openaiImageDataHasField(item, "b64_json") {
				b64Sizes = append(b64Sizes, sz)
			}
			return true
		})
		if len(urlSizes) >= len(b64Sizes) {
			sizes = urlSizes
		} else {
			sizes = b64Sizes
		}
	}
	if n := len(sizes); n > 0 && n <= dto.MaxImageN {
		info.ImageBillingFacts.ActualImageCount = n
		info.ImageBillingFacts.OutputSizes = sizes
		return
	}
	if n := forkExtractUsageImageCount(responseBody); n > 0 {
		info.ImageBillingFacts.ActualImageCount = n
	}
}

type forkImageStreamCollector struct {
	partialSizes          []string
	completedPayloadSizes []string
	completedEventSizes   []string
	usageImageCount       int
}

func (fc *forkImageStreamCollector) observe(raw []byte) {
	if len(raw) == 0 {
		return
	}
	eventType := strings.TrimSpace(ojson.GetBytesNoCopy(raw, "type").String())
	if eventType == "" {
		return
	}
	hasPayload := forkRawHasNonEmptyString(raw, "url") || forkRawHasNonEmptyString(raw, "b64_json")
	sz := strings.TrimSpace(ojson.GetBytesNoCopy(raw, "size").String())
	switch eventType {
	case "image_generation.partial_succeeded":
		if hasPayload {
			fc.partialSizes = append(fc.partialSizes, sz)
		}
	case "image_generation.completed", "image_edit.completed":
		fc.completedEventSizes = append(fc.completedEventSizes, sz)
		if hasPayload {
			fc.completedPayloadSizes = append(fc.completedPayloadSizes, sz)
		}
		if n := forkExtractUsageImageCount(raw); n > 0 {
			fc.usageImageCount = n
		}
	}
}

func (fc *forkImageStreamCollector) apply(info *relaycommon.RelayInfo, upstreamFinished bool) {
	if info == nil || info.ImageBillingFacts == nil {
		return
	}
	var sizes []string
	switch {
	case len(fc.partialSizes) > 0:
		sizes = fc.partialSizes
	case len(fc.completedPayloadSizes) > 0:
		sizes = fc.completedPayloadSizes
	default:
		sizes = fc.completedEventSizes
	}
	count := len(sizes)
	if count == 0 && fc.usageImageCount > 0 {
		count = fc.usageImageCount
	}
	if count <= 0 || count > dto.MaxImageN {
		return
	}
	if !upstreamFinished && count <= info.RequestedImageCount() {
		return
	}
	info.ImageBillingFacts.ActualImageCount = count
	if len(sizes) > 0 {
		info.ImageBillingFacts.OutputSizes = sizes
	}
}

func forkExtractSizeString(item ojson.Result) string {
	v := item.Get("size")
	if v.Type != ojson.String {
		return ""
	}
	return strings.TrimSpace(v.String())
}

func forkRawHasNonEmptyString(raw []byte, path string) bool {
	v := ojson.GetBytesNoCopy(raw, path)
	return v.Type == ojson.String && v.Raw != `""`
}

func forkExtractUsageImageCount(raw []byte) int {
	for _, path := range []string{"usage.generated_images", "usage.output_images"} {
		v := ojson.GetBytesNoCopy(raw, path)
		if v.Type == ojson.Number {
			n := v.Int()
			if n > 0 && n <= int64(dto.MaxImageN) {
				return int(n)
			}
		}
	}
	return 0
}

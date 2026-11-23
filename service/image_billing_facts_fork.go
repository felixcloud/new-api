package service

// yunai fork: 同步出图链路在所有计费模式(含 ratio 模式)下把实际出图张数、参考图张数、
// 请求 size/quality 以及按总像素分档的输出图计数写入消费日志 other, 供外部业务系统评级。

import (
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// forkImageStdMaxPixels 标准像素档上限(261 万像素, 对应火山方舟 Seedream 5.5 分档阈值:
// 总像素 <= 261 万 0.13 元/张, > 261 万且 <= 463 万 0.23 元/张)。
const forkImageStdMaxPixels int64 = 2_610_000

// ForkAnnotateImageLog 为同步出图消费日志补齐计费事实, 不修改任何额度计算结果。
func ForkAnnotateImageLog(relayInfo *relaycommon.RelayInfo, other *model.LogOther) {
	if relayInfo == nil || relayInfo.ImageBillingFacts == nil || other == nil {
		return
	}
	f := relayInfo.ImageBillingFacts
	imageCount := f.ActualImageCount
	if imageCount <= 0 && relayInfo.BillingImageCount != nil && *relayInfo.BillingImageCount > 0 {
		imageCount = *relayInfo.BillingImageCount
	}
	if imageCount <= 0 {
		imageCount = max(relayInfo.RequestedImageCount(), 1)
	}

	other.SetPublic("image_count", imageCount)
	other.SetPublic("reference_images", f.ReferenceImages)
	if f.Size != "" {
		other.SetPublic("size", f.Size)
	}
	if f.Quality != "" {
		other.SetPublic("quality", f.Quality)
	}

	stdCount, hdCount := forkClassifyImageOutputTiers(imageCount, f.OutputSizes, f.Size)
	other.SetPublic("usage_facts", map[string]any{
		"image_count":       imageCount,
		"reference_images":  f.ReferenceImages,
		"output_images_std": stdCount,
		"output_images_hd":  hdCount,
	})
}

func forkClassifyImageOutputTiers(imageCount int, outputSizes []string, fallbackSize string) (stdCount, hdCount int) {
	for i := range imageCount {
		sz := ""
		if i < len(outputSizes) {
			sz = outputSizes[i]
		}
		if sz == "" {
			sz = fallbackSize
		}
		if forkIsStdImageSize(sz) {
			stdCount++
		} else {
			hdCount++
		}
	}
	return stdCount, hdCount
}

// forkIsStdImageSize 判断单张输出图是否落在标准像素档(总像素 <= 261 万)。
// 识别显式 WxH 像素与 1K / 1.5K 符号档位; 2K / 3K / 4K / auto / 未知尺寸保守归入高档以防少收。
func forkIsStdImageSize(size string) bool {
	s := strings.ToLower(strings.TrimSpace(size))
	if s == "" {
		return false
	}
	switch s {
	case "1k", "1.5k":
		return true
	case "2k", "3k", "4k", "auto":
		return false
	}
	wStr, hStr, ok := strings.Cut(s, "x")
	if !ok {
		wStr, hStr, ok = strings.Cut(s, "*")
	}
	if !ok {
		return false
	}
	w, errW := strconv.ParseInt(strings.TrimSpace(wStr), 10, 64)
	h, errH := strconv.ParseInt(strings.TrimSpace(hStr), 10, 64)
	if errW != nil || errH != nil || w <= 0 || h <= 0 || w > 100_000 || h > 100_000 {
		return false
	}
	return w*h <= forkImageStdMaxPixels
}

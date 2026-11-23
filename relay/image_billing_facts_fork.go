package relay

// yunai fork: 同步出图链路在每次尝试开始时采集请求侧计费事实(参考图张数、size、quality),
// 重置响应侧事实, 避免渠道重试时串用上一次尝试的数据。

import (
	"encoding/json"
	"strings"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
)

func forkInitImageBillingFacts(c *gin.Context, info *relaycommon.RelayInfo, req *dto.ImageRequest) {
	if info == nil || req == nil {
		return
	}
	info.ImageBillingFacts = &relaycommon.ImageBillingFacts{
		ReferenceImages: forkCountImageReferences(c, req),
		Size:            strings.TrimSpace(req.Size),
		Quality:         strings.TrimSpace(req.Quality),
	}
}

func forkCountImageReferences(c *gin.Context, req *dto.ImageRequest) int {
	total := forkCountRawImageRefs(req.Image) + forkCountRawImageRefs(req.Images)
	if req.Extra != nil {
		total += forkCountRawImageRefs(req.Extra["image_urls"])
	}
	if c != nil && c.Request != nil && c.Request.MultipartForm != nil && c.Request.MultipartForm.File != nil {
		for field, files := range c.Request.MultipartForm.File {
			if field == "image" || field == "image[]" || field == "images" || field == "images[]" ||
				strings.HasPrefix(field, "image[") || strings.HasPrefix(field, "images[") {
				total += len(files)
			}
		}
	}
	return total
}

func forkCountRawImageRefs(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var val any
	if err := common.Unmarshal(raw, &val); err != nil || val == nil {
		return 0
	}
	switch v := val.(type) {
	case string:
		if strings.TrimSpace(v) != "" {
			return 1
		}
		return 0
	case []any:
		n := 0
		for _, item := range v {
			switch elem := item.(type) {
			case string:
				if strings.TrimSpace(elem) != "" {
					n++
				}
			case map[string]any:
				if len(elem) > 0 {
					n++
				}
			}
		}
		return n
	case map[string]any:
		if len(v) > 0 {
			return 1
		}
		return 0
	default:
		return 0
	}
}

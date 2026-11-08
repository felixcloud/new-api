package middleware

import (
	"bytes"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// DetectAssetProviderFromBody inspects a video generation JSON payload for
// channel-specific asset:// URIs or explicit metadata.asset_provider:
//   - metadata.asset_provider in ("zchmy", "volc_official") -> that provider
//   - "asset://om-a-..." -> "zchmy"
//   - otherwise -> "" (Note: both zchmy and volc_official use "asset://asset-...",
//     so "asset://asset-..." alone must not exclude zchmy channels).
func DetectAssetProviderFromBody(body []byte) string {
	if len(body) == 0 || !bytes.Contains(body, []byte("asset://")) {
		return ""
	}
	if bytes.Contains(body, []byte(`"asset_provider"`)) {
		var m map[string]any
		if err := common.Unmarshal(body, &m); err == nil {
			if p, ok := m["asset_provider"].(string); ok {
				p = strings.TrimSpace(strings.ToLower(p))
				if p == model.AssetProviderZchmy || p == model.AssetProviderVolcOfficial {
					return p
				}
			}
			if meta, ok := m["metadata"].(map[string]any); ok {
				if p, ok := meta["asset_provider"].(string); ok {
					p = strings.TrimSpace(strings.ToLower(p))
					if p == model.AssetProviderZchmy || p == model.AssetProviderVolcOfficial {
						return p
					}
				}
			}
		}
	}
	if bytes.Contains(body, []byte("asset://om-a-")) {
		return model.AssetProviderZchmy
	}
	return ""
}

func forkApplyAssetProviderConstraint(c *gin.Context) {
	if c == nil || c.Request == nil || c.Request.Method != http.MethodPost {
		return
	}
	path := c.Request.URL.Path
	if !strings.HasPrefix(path, "/v1/video") {
		return
	}
	provider := strings.TrimSpace(strings.ToLower(c.GetHeader("X-Asset-Provider")))
	if provider != model.AssetProviderZchmy && provider != model.AssetProviderVolcOfficial {
		provider = ""
	}
	if provider == "" {
		storage, err := common.GetBodyStorage(c)
		if err != nil || storage == nil {
			return
		}
		body, err := storage.Bytes()
		if err != nil {
			return
		}
		provider = DetectAssetProviderFromBody(body)
	}
	if provider == "" {
		return
	}
	constraints := service.GetChannelConstraints(c)
	for _, f := range constraints.Filters {
		if f.Kind == dto.FilterAssetProvider && f.AssetProvider == provider {
			return
		}
	}
	constraints.AddFilter(dto.ChannelFilter{
		Kind:          dto.FilterAssetProvider,
		AssetProvider: provider,
	})
}

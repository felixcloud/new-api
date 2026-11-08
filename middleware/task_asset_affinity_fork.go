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
// channel-specific asset:// URIs:
//   - "asset://om-a-..." -> "zchmy"
//   - "asset://asset-..." -> "volc_official"
//   - no asset:// URI -> ""
func DetectAssetProviderFromBody(body []byte) string {
	if len(body) == 0 || !bytes.Contains(body, []byte("asset://")) {
		return ""
	}
	if bytes.Contains(body, []byte("asset://om-a-")) {
		return model.AssetProviderZchmy
	}
	if bytes.Contains(body, []byte("asset://asset-")) {
		return model.AssetProviderVolcOfficial
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
	storage, err := common.GetBodyStorage(c)
	if err != nil || storage == nil {
		return
	}
	body, err := storage.Bytes()
	if err != nil {
		return
	}
	provider := DetectAssetProviderFromBody(body)
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

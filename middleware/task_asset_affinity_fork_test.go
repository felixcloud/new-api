package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func TestDetectAssetProviderFromBody(t *testing.T) {
	volcBody := []byte(`{"model":"doubao-seedance-2-0-260128","metadata":{"asset_provider":"volc_official","content":[{"type":"image_url","image_url":{"url":"asset://asset-20260408142310-abcde"}}]}}`)
	if got := DetectAssetProviderFromBody(volcBody); got != model.AssetProviderVolcOfficial {
		t.Fatalf("expected %q for metadata.asset_provider=volc_official, got %q", model.AssetProviderVolcOfficial, got)
	}

	zchmyExplicit := []byte(`{"model":"doubao-seedance-2-0-260128","metadata":{"asset_provider":"zchmy","content":[{"type":"image_url","image_url":{"url":"asset://asset-20261108193046-pun1k"}}]}}`)
	if got := DetectAssetProviderFromBody(zchmyExplicit); got != model.AssetProviderZchmy {
		t.Fatalf("expected %q for metadata.asset_provider=zchmy, got %q", model.AssetProviderZchmy, got)
	}

	ambiguousAsset := []byte(`{"model":"doubao-seedance-2-0-260128","metadata":{"content":[{"type":"image_url","image_url":{"url":"asset://asset-20261108193046-pun1k"}}]}}`)
	if got := DetectAssetProviderFromBody(ambiguousAsset); got != "" {
		t.Fatalf("expected empty provider for ambiguous asset://asset-* without asset_provider, got %q", got)
	}

	zchmyBody := []byte(`{"model":"doubao-seedance-2-0-260128","metadata":{"content":[{"type":"image_url","image_url":{"url":"asset://om-a-019d70a7f6d27297"}}]}}`)
	if got := DetectAssetProviderFromBody(zchmyBody); got != model.AssetProviderZchmy {
		t.Fatalf("expected %q for asset://om-a-*, got %q", model.AssetProviderZchmy, got)
	}

	httpBody := []byte(`{"model":"doubao-seedance-2-0-260128","metadata":{"content":[{"type":"image_url","image_url":{"url":"https://example.com/a.png"}}]}}`)
	if got := DetectAssetProviderFromBody(httpBody); got != "" {
		t.Fatalf("expected empty provider for https url, got %q", got)
	}
}

func TestChannelSatisfiesAssetProviderFilter(t *testing.T) {
	volcBase := "https://ark.cn-beijing.volces.com"
	volcCh := &model.Channel{
		Id:      1,
		Name:    "volc-ark-main",
		BaseURL: &volcBase,
	}
	zchmyBase := "https://zchmy.top"
	zchmyCh := &model.Channel{
		Id:      2,
		Name:    "zchmy-seedance",
		BaseURL: &zchmyBase,
	}

	volcFilter := []dto.ChannelFilter{{Kind: dto.FilterAssetProvider, AssetProvider: model.AssetProviderVolcOfficial}}
	zchmyFilter := []dto.ChannelFilter{{Kind: dto.FilterAssetProvider, AssetProvider: model.AssetProviderZchmy}}

	if ok, _ := model.ChannelSatisfiesFilters(volcCh, "doubao-seedance-2-0-260128", volcFilter); !ok {
		t.Fatalf("expected volc channel to satisfy volc_official filter")
	}
	if ok, _ := model.ChannelSatisfiesFilters(zchmyCh, "doubao-seedance-2-0-260128", volcFilter); ok {
		t.Fatalf("expected zchmy channel to be rejected by volc_official filter")
	}
	if ok, _ := model.ChannelSatisfiesFilters(zchmyCh, "doubao-seedance-2-0-260128", zchmyFilter); !ok {
		t.Fatalf("expected zchmy channel to satisfy zchmy filter")
	}
	if ok, _ := model.ChannelSatisfiesFilters(volcCh, "doubao-seedance-2-0-260128", zchmyFilter); ok {
		t.Fatalf("expected volc channel to be rejected by zchmy filter")
	}
}

func TestForkApplyAssetProviderConstraint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	payload := `{"model":"doubao-seedance-2-0-260128","metadata":{"content":[{"type":"image_url","image_url":{"url":"asset://om-a-123456"}}]}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/video/generations", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req

	forkApplyAssetProviderConstraint(c)
	constraints := service.GetChannelConstraints(c)
	found := false
	for _, f := range constraints.Filters {
		if f.Kind == dto.FilterAssetProvider && f.AssetProvider == model.AssetProviderZchmy {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected FilterAssetProvider=zchmy to be added to constraints, got %+v", constraints.Filters)
	}
}

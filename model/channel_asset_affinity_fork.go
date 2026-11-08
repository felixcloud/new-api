package model

import "strings"

const (
	AssetProviderVolcOfficial = "volc_official"
	AssetProviderZchmy        = "zchmy"
)

func forkIsZchmyChannel(ch *Channel) bool {
	if ch == nil {
		return false
	}
	if strings.Contains(strings.ToLower(ch.GetBaseURL()), "zchmy") {
		return true
	}
	if strings.Contains(strings.ToLower(ch.Name), "zchmy") {
		return true
	}
	if strings.Contains(strings.ToLower(ch.GetTag()), "zchmy") {
		return true
	}
	return false
}

func forkChannelMatchesAssetProvider(ch *Channel, provider string) bool {
	if ch == nil {
		return false
	}
	switch provider {
	case "":
		return true
	case AssetProviderZchmy:
		return forkIsZchmyChannel(ch)
	case AssetProviderVolcOfficial:
		return !forkIsZchmyChannel(ch)
	default:
		return true
	}
}

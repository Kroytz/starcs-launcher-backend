package stardustcatalog

import (
	"strings"
	"testing"
)

func TestParseFlattensCatalogAndInheritsRestriction(t *testing.T) {
	items, err := Parse(strings.NewReader(`{
		"Items": {
			"个性化": {"聊天颜色": {"红色": {"uniqueid":"chatcolorred","type":"chatcolor","price":"1288","slot":"1"}}},
			"个人专属": {"flag":"76561198000000001","专属语音":{"uniqueid":"voice_private","type":"cheer","price":"0","slot":"1"}}
		}
	}`))
	if err != nil {
		t.Fatalf("parse catalog: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	byID := map[string]Item{}
	for _, item := range items {
		byID[item.UniqueID] = item
	}
	public := byID["chatcolorred"]
	if public.Group != "个性化" || public.Category != "聊天颜色" || !public.Purchasable || public.Price != 1288 {
		t.Fatalf("unexpected public item: %+v", public)
	}
	private := byID["voice_private"]
	if private.RestrictedSteamID != "76561198000000001" || private.Purchasable {
		t.Fatalf("unexpected private item: %+v", private)
	}
}

func TestParseRejectsDuplicateTypeAndUniqueID(t *testing.T) {
	_, err := Parse(strings.NewReader(`{"Items":{"A":{"One":{"uniqueid":"same","type":"voice"}},"B":{"Two":{"uniqueid":"same","type":"voice"}}}}`))
	if err == nil {
		t.Fatal("expected duplicate item error")
	}
}

package mysqlrepo

import "testing"

func TestAnnouncementSummaryUsesFirstTextBlock(t *testing.T) {
	raw := `{"sections":[{"title":"更新说明","blocks":[{"kind":"paragraph","text":"第一段公告内容"}]}]}`
	if got := announcementSummary(raw); got != "第一段公告内容" {
		t.Fatalf("unexpected summary %q", got)
	}
}

func TestAnnouncementPayloadPreservesValidJSONAndRejectsInvalidInput(t *testing.T) {
	valid := `{"sections":[{"title":"更新"}]}`
	if got := string(announcementPayload(valid)); got != valid {
		t.Fatalf("unexpected payload %q", got)
	}
	if got := string(announcementPayload("not-json")); got != "{}" {
		t.Fatalf("invalid payload should become an empty object, got %q", got)
	}
}

func TestPublicFileURLUsesStaticAssetHost(t *testing.T) {
	if got := publicFileURL("/images/announcement/detail.png"); got != "https://static.starcs.cn/images/announcement/detail.png" {
		t.Fatalf("unexpected public file URL %q", got)
	}
	absolute := "https://cdn.example.com/detail.png"
	if got := publicFileURL(absolute); got != absolute {
		t.Fatalf("absolute URL should be preserved, got %q", got)
	}
}

func TestCollectionCountSupportsJSONAndDelimitedText(t *testing.T) {
	tests := map[string]int{
		`[]`:          0,
		`[1,2,3]`:     3,
		`{"a":1}`:     1,
		`ZM,ZE,TTT`:   3,
		`reward#next`: 2,
	}
	for input, expected := range tests {
		if got := collectionCount(input); got != expected {
			t.Errorf("collectionCount(%q)=%d, expected %d", input, got, expected)
		}
	}
}

func TestDisplayTypeMapsItemCardsAndCharacters(t *testing.T) {
	if category, icon := displayType("ItemCard", 1); category != "道具卡" || icon != "package" {
		t.Fatalf("unexpected ItemCard mapping: %q %q", category, icon)
	}
	if category, icon := displayType("Character", 2); category != "集字" || icon != "trophy" {
		t.Fatalf("unexpected Character mapping: %q %q", category, icon)
	}
	if category, icon := displayType("PlayerSkin", 1); category != "角色外观" || icon != "user-round" {
		t.Fatalf("unexpected PlayerSkin mapping: %q %q", category, icon)
	}
}

func TestInventoryQuantityTreatsPermanentRowsAsOwned(t *testing.T) {
	if got := inventoryQuantity(0); got != 1 {
		t.Fatalf("permanent inventory row should display as one item, got %d", got)
	}
	if got := inventoryQuantity(4); got != 4 {
		t.Fatalf("stacked inventory quantity should be preserved, got %d", got)
	}
}

func TestWeaponModelTypeNameMatchesStarLightStoreEnum(t *testing.T) {
	tests := map[int]string{
		0:  "",
		1:  "Knife",
		4:  "SubMachineGun",
		10: "CommonRifle",
		14: "StackableItem",
		99: "",
	}
	for value, want := range tests {
		if got := weaponModelTypeName(value); got != want {
			t.Fatalf("weaponModelTypeName(%d) = %q, want %q", value, got, want)
		}
	}
}

func TestAfdianStoreMetadata(t *testing.T) {
	if got := afdianPurchaseURL(" 530b6328383811efab485254001e7c00 "); got != "https://www.ifdian.net/item/530b6328383811efab485254001e7c00" {
		t.Fatalf("unexpected afdian purchase URL %q", got)
	}
	if category, icon := afdianCategory("vip,svip", 0); category != "会员" || icon != "star" {
		t.Fatalf("unexpected afdian category: %q %q", category, icon)
	}
	if category, _ := afdianCategory("", 1); category != "道具卡" {
		t.Fatalf("unexpected prefab category %q", category)
	}
}

func TestChallengeCategoryAndTitle(t *testing.T) {
	category, icon := challengeCategory("chatcolor")
	if category != "聊天颜色" || icon != "sparkles" {
		t.Fatalf("unexpected challenge category: %q %q", category, icon)
	}
	if got := challengeItemTitle("chatcolor", "chatcolorblue"); got != "聊天颜色 · 蓝色" {
		t.Fatalf("unexpected challenge title %q", got)
	}
	if got := challengeItemTitle("death_voice", "death_voice_2"); got != "死亡语音 · 2" {
		t.Fatalf("unexpected challenge voice title %q", got)
	}
}

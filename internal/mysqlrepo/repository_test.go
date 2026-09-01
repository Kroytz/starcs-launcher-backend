package mysqlrepo

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/starcs/star-launcher-backend/internal/domain"
)

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

type fakeGroupMembership struct {
	allowed map[uint64]bool
}

func (f fakeGroupMembership) IsMember(_ context.Context, groupID, _ uint64, _ int) bool {
	return f.allowed[groupID]
}

func TestFilterExclusiveInventoryMatchesPersonalAndSteamGroupEntitlements(t *testing.T) {
	const steamID = uint64(76561198000000001)
	repository := &Repository{groupMembership: fakeGroupMembership{allowed: map[uint64]bool{42: true}}}
	items := []domain.InventoryItem{
		{ProductID: 1, UseLimit: 2},
		{ProductID: 2, UseLimit: 8, UseLimitInfo: "76561198000000001"},
		{ProductID: 3, UseLimit: 8, UseLimitInfo: "76561198000000002"},
		{ProductID: 4, UseLimit: 4, UseLimitInfo: "42, 10"},
		{ProductID: 5, UseLimit: 4, UseLimitInfo: "43, 10"},
		{ProductID: 6, UseLimit: 4, UseLimitInfo: "invalid"},
	}

	filtered := repository.filterExclusiveInventory(context.Background(), steamID, items)
	if len(filtered) != 3 {
		t.Fatalf("expected three eligible items, got %+v", filtered)
	}
	for index, productID := range []int64{1, 2, 4} {
		if filtered[index].ProductID != productID {
			t.Fatalf("filtered[%d].ProductID=%d, want %d", index, filtered[index].ProductID, productID)
		}
	}
}

func TestParseSteamGroupLimitUsesStarLightStoreFormat(t *testing.T) {
	groupID, maxMembers, ok := parseSteamGroupLimit(" 103582791429521412, 25 ")
	if !ok || groupID != 103582791429521412 || maxMembers != 25 {
		t.Fatalf("unexpected parsed group limit: group=%d max=%d ok=%v", groupID, maxMembers, ok)
	}
	for _, invalid := range []string{"", "42", "42,0", "bad,10", "42,10,extra"} {
		if _, _, ok := parseSteamGroupLimit(invalid); ok {
			t.Fatalf("expected %q to be rejected", invalid)
		}
	}
}

func TestFormatExpiryTreatsNullAndSentinelAsPermanent(t *testing.T) {
	if got := formatExpiry(sql.NullTime{}); got != "" {
		t.Fatalf("NULL expiry should display as permanent, got %q", got)
	}
	sentinel := time.Date(9000, 1, 1, 0, 0, 0, 0, time.Local)
	if got := formatExpiry(sql.NullTime{Time: sentinel, Valid: true}); got != "" {
		t.Fatalf("9000 sentinel should display as permanent, got %q", got)
	}
	timed := time.Date(2030, 6, 1, 12, 0, 0, 0, time.UTC)
	if got := formatExpiry(sql.NullTime{Time: timed, Valid: true}); got != timed.Format(time.RFC3339) {
		t.Fatalf("timed expiry should keep RFC3339, got %q", got)
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

func TestWorkshopPageURL(t *testing.T) {
	if got := workshopPageURL(" 3711721516 "); got != "https://steamcommunity.com/workshop/filedetails/?id=3711721516" {
		t.Fatalf("unexpected workshop page URL %q", got)
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

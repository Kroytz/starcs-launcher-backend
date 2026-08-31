package domain

import (
	"encoding/json"
	"time"
)

type Announcement struct {
	ID             string          `json:"id"`
	Title          string          `json:"title"`
	Content        string          `json:"content"`
	Level          string          `json:"level"`
	Dismissible    bool            `json:"dismissible"`
	DisplayDate    string          `json:"displayDate"`
	PublishedAt    string          `json:"publishedAt"`
	CoverImageURL  string          `json:"coverImageUrl"`
	DetailImageURL string          `json:"detailImageUrl"`
	RenderPayload  json.RawMessage `json:"renderPayload"`
}

type Wallet struct {
	StarCoin           int64 `json:"starCoin"`
	Starlight          int64 `json:"starlight"`
	Stardust           int64 `json:"stardust"`
	StarCoinAvailable  bool  `json:"starCoinAvailable"`
	StarlightAvailable bool  `json:"starlightAvailable"`
	StardustAvailable  bool  `json:"stardustAvailable"`
}

type ExchangeRate struct {
	From string `json:"from"`
	To   string `json:"to"`
	Rate int64  `json:"rate"`
}

type StoreItem struct {
	ID              string `json:"id"`
	ExternalID      string `json:"externalId"`
	Currency        string `json:"currency"`
	Category        string `json:"category"`
	PurchaseBackend string `json:"purchaseBackend"`
	PurchaseURL     string `json:"purchaseUrl"`
	Title           string `json:"title"`
	Description     string `json:"description"`
	Price           int64  `json:"price"`
	Days            int    `json:"days"`
	Quantity        int    `json:"quantity"`
	Icon            string `json:"icon"`
	Tone            string `json:"tone"`
	Tag             string `json:"tag"`
	Enabled         bool   `json:"enabled"`
	Sort            int    `json:"sort"`
	ImageURL        string `json:"imageUrl"`
	StardustType    string `json:"stardustType,omitempty"`
}

type InventoryItem struct {
	ProductID    int64  `json:"productId"`
	ID           string `json:"id"`
	Source       string `json:"source"`
	UniqueID     string `json:"uniqueId"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	Rarity       string `json:"rarity"`
	Quantity     int64  `json:"quantity"`
	Icon         string `json:"icon"`
	Tone         string `json:"tone"`
	AcquiredAt   string `json:"acquiredAt"`
	ExpiresAt    string `json:"expiresAt"`
	Description  string `json:"description"`
	Mode         string `json:"mode"`
	UseLimit     int    `json:"useLimit"`
	UseLimitInfo string `json:"useLimitInfo"`
	WeaponPrefab string `json:"weaponPrefab"`
	WeaponType   string `json:"weaponType"`
	Equipped     bool   `json:"equipped"`
	StardustType string `json:"stardustType,omitempty"`
}

// StardustEquipment 表示 db_challenge 中一件已装备的星尘物品。
type StardustEquipment struct {
	Type     string `json:"type"`
	UniqueID string `json:"uniqueId"`
	Slot     int    `json:"slot"`
}

// StarlightPurchaseResult 星光商城购买成功后的最新账号状态。
type StarlightPurchaseResult struct {
	Starlight       int64                 `json:"starlight"`
	Inventory       []InventoryItem       `json:"inventory"`
	PurchaseHistory []PurchaseHistoryItem `json:"purchaseHistory"`
	StoreItems      []StoreItem           `json:"storeItems"`
}

// StardustPurchaseResult 星尘商店购买成功后的最新账号状态。
type StardustPurchaseResult struct {
	Stardust   int64           `json:"stardust"`
	Inventory  []InventoryItem `json:"inventory"`
	StoreItems []StoreItem     `json:"storeItems"`
}

type Profile struct {
	UserID         string `json:"userId"`
	DisplayName    string `json:"displayName"`
	Verified       bool   `json:"verified"`
	MemberLevel    int    `json:"memberLevel"`
	CommunityLevel int    `json:"communityLevel"`
	PlayHours      int    `json:"playHours"`
	Achievements   int    `json:"achievements"`
	SteamConnected bool   `json:"steamConnected"`
	AvatarURL      string `json:"avatarUrl"`
}

type MapResource struct {
	ID          uint64 `json:"id"`
	Name        string `json:"name"`
	ShortName   string `json:"shortName"`
	WorkshopID  string `json:"workshopId"`
	Difficulty  string `json:"difficulty"`
	Description string `json:"description"`
}

type WorkshopPack struct {
	ID          uint64 `json:"id"`
	Kind        string `json:"kind"`
	Mode        string `json:"mode"`
	Title       string `json:"title"`
	Description string `json:"description"`
	WorkshopID  string `json:"workshopId"`
	WorkshopURL string `json:"workshopUrl"`
	SteamURL    string `json:"steamUrl"`
}

type PurchaseHistoryItem struct {
	ID           uint64 `json:"id"`
	ProductName  string `json:"productName"`
	CurrencyType string `json:"currencyType"`
	Quantity     uint64 `json:"quantity"`
	Days         int    `json:"days"`
	TotalPrice   int64  `json:"totalPrice"`
	State        int    `json:"state"`
	Description  string `json:"description"`
	CreatedAt    string `json:"createdAt"`
}

type SeasonPassOverview struct {
	Available             bool           `json:"available"`
	SeasonID              int            `json:"seasonId"`
	PassType              int            `json:"passType"`
	Level                 int            `json:"level"`
	Experience            int            `json:"experience"`
	ClaimedRewardCount    int            `json:"claimedRewardCount"`
	StarSourceChestOpened int            `json:"starSourceChestOpened"`
	DailyGames            int            `json:"dailyGames"`
	DailyOnlineMinutes    int            `json:"dailyOnlineMinutes"`
	DailyLoggedIn         bool           `json:"dailyLoggedIn"`
	WeeklyGames           int            `json:"weeklyGames"`
	WeeklyCompletedModes  int            `json:"weeklyCompletedModes"`
	WeeklyLoggedIn        bool           `json:"weeklyLoggedIn"`
	DailyQuestStatus      map[string]int `json:"dailyQuestStatus"`
	WeeklyQuestStatus     map[string]int `json:"weeklyQuestStatus"`
	UpdatedAt             string         `json:"updatedAt"`
}

type AccountPenalty struct {
	Type      string `json:"type"`
	Reason    string `json:"reason"`
	Mode      string `json:"mode"`
	Permanent bool   `json:"permanent"`
	ExpiresAt string `json:"expiresAt"`
	CreatedAt string `json:"createdAt"`
}

type PlayerReadModel struct {
	Account         AccountOverview       `json:"account"`
	Inventory       []InventoryItem       `json:"inventory"`
	PurchaseHistory []PurchaseHistoryItem `json:"purchaseHistory"`
	SeasonPass      SeasonPassOverview    `json:"seasonPass"`
	Penalties       []AccountPenalty      `json:"penalties"`
	StoreItems      []StoreItem           `json:"storeItems"`
}

type AppConfig struct {
	Name            string `json:"name"`
	WebsiteURL      string `json:"websiteUrl"`
	RechargeEnabled bool   `json:"rechargeEnabled"`
}

type AccountOverview struct {
	Profile       Profile        `json:"profile"`
	Wallet        Wallet         `json:"wallet"`
	ExchangeRates []ExchangeRate `json:"exchangeRates"`
}

type Bootstrap struct {
	App           AppConfig       `json:"app"`
	Announcements []Announcement  `json:"announcements"`
	Account       AccountOverview `json:"account"`
	StoreItems    []StoreItem     `json:"storeItems"`
	Inventory     []InventoryItem `json:"inventory"`
	Maps          []MapResource   `json:"maps"`
}

// LauncherRelease 表示一条启动器发布记录（自更新用）。
type LauncherRelease struct {
	ID          uint64    `json:"id"`
	Version     string    `json:"version"`
	Mandatory   bool      `json:"mandatory"`
	Changelog   string    `json:"changelog"`
	ArtifactURL string    `json:"artifactUrl"`
	Signature   string    `json:"-"` // 仅 manifest 端点输出，策略端点不暴露
	PubDate     time.Time `json:"pubDate"`
}

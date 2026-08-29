package domain

type Announcement struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Content     string `json:"content"`
	Level       string `json:"level"`
	Dismissible bool   `json:"dismissible"`
	DisplayDate string `json:"displayDate"`
	PublishedAt string `json:"publishedAt"`
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
	Title           string `json:"title"`
	Description     string `json:"description"`
	Price           int64  `json:"price"`
	Icon            string `json:"icon"`
	Tone            string `json:"tone"`
	Tag             string `json:"tag"`
	Enabled         bool   `json:"enabled"`
	Sort            int    `json:"sort"`
	ImageURL        string `json:"imageUrl"`
}

type InventoryItem struct {
	ProductID  int64  `json:"productId"`
	ID         string `json:"id"`
	Source     string `json:"source"`
	UniqueID   string `json:"uniqueId"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Rarity     string `json:"rarity"`
	Quantity   int64  `json:"quantity"`
	Icon       string `json:"icon"`
	Tone       string `json:"tone"`
	AcquiredAt string `json:"acquiredAt"`
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
	Available             bool   `json:"available"`
	SeasonID              int    `json:"seasonId"`
	PassType              int    `json:"passType"`
	Level                 int    `json:"level"`
	Experience            int    `json:"experience"`
	ClaimedRewardCount    int    `json:"claimedRewardCount"`
	StarSourceChestOpened int    `json:"starSourceChestOpened"`
	DailyGames            int    `json:"dailyGames"`
	DailyOnlineMinutes    int    `json:"dailyOnlineMinutes"`
	WeeklyGames           int    `json:"weeklyGames"`
	WeeklyCompletedModes  int    `json:"weeklyCompletedModes"`
	UpdatedAt             string `json:"updatedAt"`
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

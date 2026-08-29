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
	StarCoin  int64 `json:"starCoin"`
	Starlight int64 `json:"starlight"`
	Stardust  int64 `json:"stardust"`
}

type ExchangeRate struct {
	From string `json:"from"`
	To   string `json:"to"`
	Rate int64  `json:"rate"`
}

type StoreItem struct {
	ID          string `json:"id"`
	Currency    string `json:"currency"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Price       int64  `json:"price"`
	Icon        string `json:"icon"`
	Tone        string `json:"tone"`
	Tag         string `json:"tag"`
	Enabled     bool   `json:"enabled"`
	Sort        int    `json:"sort"`
}

type InventoryItem struct {
	ProductID  int64  `json:"productId"`
	ID         string `json:"id"`
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
}

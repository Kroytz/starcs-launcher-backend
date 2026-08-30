package demo

import (
	"sort"
	"strings"

	"github.com/starcs/star-launcher-backend/internal/domain"
)

type Store struct {
	app           domain.AppConfig
	announcements []domain.Announcement
	account       domain.AccountOverview
	storeItems    []domain.StoreItem
	inventory     []domain.InventoryItem
}

func NewStore() Store {
	return Store{
		app: domain.AppConfig{
			Name:            "STAR Launcher",
			WebsiteURL:      "https://www.starcs.cn",
			RechargeEnabled: false,
		},
		announcements: []domain.Announcement{
			{
				ID:          "summer-season-2026",
				Title:       "STAR 夏日赛季现已开启",
				Content:     "全新地图轮换、赛季任务与社区活动已经上线。",
				Level:       "info",
				Dismissible: true,
				DisplayDate: "08 / 29",
				PublishedAt: "2026-08-29T00:00:00+08:00",
			},
		},
		account: domain.AccountOverview{
			Profile: domain.Profile{
				UserID:         "demo-traveler",
				DisplayName:    "Traveler",
				Verified:       true,
				MemberLevel:    28,
				CommunityLevel: 42,
				PlayHours:      126,
				Achievements:   18,
				SteamConnected: true,
				AvatarURL:      "https://www.starcs.cn/images/logo_64x64.png",
			},
			Wallet: domain.Wallet{
				StarCoin:           128,
				Starlight:          1260,
				Stardust:           420,
				StarCoinAvailable:  true,
				StarlightAvailable: true,
				StardustAvailable:  true,
			},
			ExchangeRates: []domain.ExchangeRate{
				{From: "starCoin", To: "starlight", Rate: 10},
				{From: "starCoin", To: "stardust", Rate: 5},
			},
		},
		storeItems: []domain.StoreItem{
			{ID: "afdian-vip", ExternalID: "demo-plan", Currency: "afdian", Category: "会员", PurchaseBackend: "afdian-cdk", PurchaseURL: "https://www.ifdian.net/item/demo-plan", Title: "VIP/月", Description: "1个月的VIP", Price: 49, Icon: "star", Tone: "from-pink-500 to-rose-600", Tag: "会员", Enabled: true, Sort: 1},
			{ID: "starlight-pass", Currency: "starlight", Title: "STAR 高级通行证", Description: "解锁赛季任务与专属奖励路线", Price: 680, Icon: "trophy", Tone: "from-primary to-secondary", Tag: "热门", Enabled: true, Sort: 10},
			{ID: "starlight-membership", Currency: "starlight", Title: "会员月卡", Description: "专属队列、经验加成与社区徽章", Price: 420, Icon: "star", Tone: "from-cyan-500 to-primary", Tag: "推荐", Enabled: true, Sort: 20},
			{ID: "starlight-name-card", Currency: "starlight", Title: "昵称炫彩卡", Description: "解锁一张可自定义的昵称渐变卡", Price: 180, Icon: "sparkles", Tone: "from-violet-500 to-fuchsia-500", Tag: "装饰", Enabled: true, Sort: 30},
			{ID: "starlight-priority", Currency: "starlight", Title: "优先队列券 × 3", Description: "高峰期获得三次优先加入机会", Price: 120, Icon: "zap", Tone: "from-blue-500 to-indigo-600", Tag: "实用", Enabled: true, Sort: 40},
			{ID: "stardust-crate", Currency: "stardust", Title: "星辉补给箱", Description: "随机获得武器外观或个人装饰", Price: 90, Icon: "gift", Tone: "from-accent to-orange-400", Tag: "新品", Enabled: true, Sort: 10},
			{ID: "stardust-keys", Currency: "stardust", Title: "补给箱钥匙 × 3", Description: "用于开启社区活动补给箱", Price: 35, Icon: "package", Tone: "from-amber-500 to-orange-600", Tag: "消耗品", Enabled: true, Sort: 20},
			{ID: "stardust-guardian", Currency: "stardust", Title: "社区守护者徽章", Description: "在个人资料中展示限定徽记", Price: 240, Icon: "shield-check", Tone: "from-emerald-500 to-cyan-500", Tag: "限定", Enabled: true, Sort: 30},
			{ID: "stardust-lucky-pack", Currency: "stardust", Title: "幸运星尘包", Description: "包含随机数量的活动兑换材料", Price: 150, Icon: "gem", Tone: "from-secondary to-violet-500", Tag: "活动", Enabled: true, Sort: 40},
		},
		inventory: []domain.InventoryItem{
			{ID: "inv-star-rifle", Name: "星轨步枪涂装", Type: "武器外观", Rarity: "史诗", Quantity: 1, Icon: "zap", Tone: "from-primary to-secondary", AcquiredAt: "2026-08-12T18:30:00+08:00"},
			{ID: "inv-starlight-agent", Name: "星光特勤队员外观", Type: "玩家外观", Rarity: "史诗", Quantity: 1, Icon: "user-round", Tone: "from-violet-500 to-primary", AcquiredAt: "2026-08-16T20:00:00+08:00"},
			{ID: "inv-summer-badge", Name: "夏日纪念徽章", Type: "个人装饰", Rarity: "稀有", Quantity: 1, Icon: "star", Tone: "from-cyan-500 to-primary", AcquiredAt: "2026-08-20T12:00:00+08:00"},
			{ID: "inv-crate-key", Name: "补给箱钥匙", Type: "消耗品", Rarity: "普通", Quantity: 3, Icon: "package", Tone: "from-accent to-orange-400", AcquiredAt: "2026-08-21T16:15:00+08:00", ExpiresAt: "2026-09-21T16:15:00+08:00", Description: "开启补给箱获得随机外观，入库后 30 天内有效。"},
			{ID: "inv-pioneer-title", Name: "先锋玩家称号", Type: "称号", Rarity: "限定", Quantity: 1, Icon: "trophy", Tone: "from-emerald-500 to-cyan-500", AcquiredAt: "2026-07-01T00:00:00+08:00"},
			{ID: "inv-season-boost", Name: "赛季经验加成卡", Type: "增益道具", Rarity: "稀有", Quantity: 1, Icon: "gift", Tone: "from-secondary to-primary", AcquiredAt: "2026-08-25T09:10:00+08:00"},
			{ID: "inv-guardian-mark", Name: "社区守护者徽记", Type: "个人装饰", Rarity: "限定", Quantity: 1, Icon: "shield-check", Tone: "from-sky-500 to-indigo-600", AcquiredAt: "2026-06-18T21:00:00+08:00"},
		},
	}
}

func (s Store) App() domain.AppConfig {
	return s.app
}

func (s Store) Announcements() []domain.Announcement {
	return append([]domain.Announcement(nil), s.announcements...)
}

func (s Store) Account() domain.AccountOverview {
	account := s.account
	account.ExchangeRates = append([]domain.ExchangeRate(nil), s.account.ExchangeRates...)
	return account
}

func (s Store) StoreItems(currency string) []domain.StoreItem {
	currency = strings.ToLower(strings.TrimSpace(currency))
	items := make([]domain.StoreItem, 0, len(s.storeItems))
	for _, item := range s.storeItems {
		if currency == "" || item.Currency == currency {
			items = append(items, item)
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Currency != items[j].Currency {
			return items[i].Currency < items[j].Currency
		}
		return items[i].Sort < items[j].Sort
	})
	return items
}

func (s Store) Inventory() []domain.InventoryItem {
	return append([]domain.InventoryItem(nil), s.inventory...)
}

func (s Store) Bootstrap() domain.Bootstrap {
	return domain.Bootstrap{
		App:           s.App(),
		Announcements: s.Announcements(),
		Account:       s.Account(),
		StoreItems:    s.StoreItems(""),
		Inventory:     s.Inventory(),
	}
}

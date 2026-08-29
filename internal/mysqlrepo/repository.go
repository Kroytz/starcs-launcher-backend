package mysqlrepo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/starcs/star-launcher-backend/internal/domain"
)

var ErrInvalidCredentials = errors.New("invalid credentials")

type Repository struct {
	db *sql.DB
}

func Open(ctx context.Context, dsn string) (*Repository, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping mysql: %w", err)
	}
	return &Repository{db: db}, nil
}

func (r *Repository) Close() error {
	return r.db.Close()
}

func (r *Repository) Authenticate(ctx context.Context, steamID uint64, password string) error {
	var matched int
	err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM scs_user AS u
			INNER JOIN scs_user_steamid AS us ON us.uid = u.uid
			WHERE us.steamid = ? AND BINARY u.password = BINARY ?
			LIMIT 1
		)
	`, steamID, password).Scan(&matched)
	if err != nil {
		return fmt.Errorf("query account: %w", err)
	}
	if matched != 1 {
		return ErrInvalidCredentials
	}
	return nil
}

func (r *Repository) Inventory(ctx context.Context, steamID uint64) ([]domain.InventoryItem, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT
			i.product_id,
			p.name,
			p.label,
			p.type,
			p.rarity_id,
			COALESCE(r.name, ''),
			i.number,
			i.created
		FROM sls_player_inventory AS i
		INNER JOIN sls_product AS p ON p.id = i.product_id
		LEFT JOIN sls_product_rarity AS r ON r.id = p.rarity_id
		WHERE i.steamid = ?
			AND i.number > 0
			AND (i.expired IS NULL OR i.expired > NOW())
			AND p.show_state IN (1, 3)
		ORDER BY p.rarity_id DESC, i.updated DESC, i.product_id ASC
	`, steamID)
	if err != nil {
		return nil, fmt.Errorf("query inventory: %w", err)
	}
	defer rows.Close()

	items := make([]domain.InventoryItem, 0)
	for rows.Next() {
		var (
			productID   int64
			name        string
			label       string
			productType int
			rarityID    int
			rarityName  string
			quantity    int64
			created     time.Time
		)
		if err := rows.Scan(&productID, &name, &label, &productType, &rarityID, &rarityName, &quantity, &created); err != nil {
			return nil, fmt.Errorf("scan inventory: %w", err)
		}
		itemType, icon := displayType(label, productType)
		items = append(items, domain.InventoryItem{
			ProductID:  productID,
			ID:         fmt.Sprintf("product-%d", productID),
			Name:       name,
			Type:       itemType,
			Rarity:     displayRarity(rarityID, rarityName),
			Quantity:   quantity,
			Icon:       icon,
			Tone:       rarityTone(rarityID),
			AcquiredAt: created.Format(time.RFC3339),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate inventory: %w", err)
	}
	return items, nil
}

func (r *Repository) Announcements(ctx context.Context) ([]domain.Announcement, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, title, render_payload, type, published_at, created
		FROM scs_announcement
		WHERE status = 1
		  AND (start_at IS NULL OR start_at <= NOW())
		  AND (end_at IS NULL OR end_at >= NOW())
		ORDER BY is_pinned DESC, priority DESC, published_at DESC, id DESC
		LIMIT 20
	`)
	if err != nil {
		return nil, fmt.Errorf("query announcements: %w", err)
	}
	defer rows.Close()
	items := make([]domain.Announcement, 0)
	for rows.Next() {
		var id uint64
		var title string
		var payload sql.NullString
		var announcementType int
		var published sql.NullTime
		var created time.Time
		if err := rows.Scan(&id, &title, &payload, &announcementType, &published, &created); err != nil {
			return nil, fmt.Errorf("scan announcement: %w", err)
		}
		publishedAt := created
		if published.Valid {
			publishedAt = published.Time
		}
		items = append(items, domain.Announcement{
			ID:          strconv.FormatUint(id, 10),
			Title:       title,
			Content:     announcementSummary(payload.String),
			Level:       map[bool]string{true: "event", false: "info"}[announcementType == 1],
			Dismissible: true,
			DisplayDate: publishedAt.Format("01 / 02"),
			PublishedAt: publishedAt.Format(time.RFC3339),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate announcements: %w", err)
	}
	return items, nil
}

func (r *Repository) StoreItems(ctx context.Context) ([]domain.StoreItem, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT pp.id, p.id, p.name, p.desc, p.label, p.type, p.rarity_id,
		       COALESCE(r.name, ''), pp.price, pp.sort, COALESCE(f.relative_path, '')
		FROM sls_product_pricing AS pp
		INNER JOIN sls_product AS p ON p.id = pp.product_id
		LEFT JOIN sls_product_rarity AS r ON r.id = p.rarity_id
		LEFT JOIN scs_sls_product_preview AS pv ON pv.id = (
			SELECT candidate.id
			FROM scs_sls_product_preview AS candidate
			WHERE candidate.product_id = p.id AND candidate.show_state = 1
			ORDER BY candidate.weight ASC, candidate.id ASC
			LIMIT 1
		)
		LEFT JOIN scs_file AS f ON f.id = pv.file_id
		WHERE pp.state = 1 AND pp.currency_id = 1
		  AND p.state = 1 AND p.show_state IN (1, 2)
		ORDER BY pp.sort ASC, p.rarity_id DESC, p.id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("query store items: %w", err)
	}
	defer rows.Close()
	items := make([]domain.StoreItem, 0)
	for rows.Next() {
		var pricingID, productID int64
		var name, description, label, rarityName, relativePath string
		var productType, rarityID, price, sortOrder int
		if err := rows.Scan(&pricingID, &productID, &name, &description, &label, &productType, &rarityID, &rarityName, &price, &sortOrder, &relativePath); err != nil {
			return nil, fmt.Errorf("scan store item: %w", err)
		}
		_, icon := displayType(label, productType)
		items = append(items, domain.StoreItem{
			ID:          fmt.Sprintf("pricing-%d", pricingID),
			Currency:    "starlight",
			Title:       name,
			Description: description,
			Price:       int64(price),
			Icon:        icon,
			Tone:        rarityTone(rarityID),
			Tag:         displayRarity(rarityID, rarityName),
			Enabled:     true,
			Sort:        sortOrder,
			ImageURL:    publicFileURL(relativePath),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate store items: %w", err)
	}
	return items, nil
}

func (r *Repository) Maps(ctx context.Context) ([]domain.MapResource, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT m.id, m.name, m.short_name, m.workshop_id, d.name, m.desc
		FROM scs_cs2_map AS m
		INNER JOIN scs_cs2_map_difficulty AS d ON d.id = m.difficulty_id
		WHERE m.state = 1
		ORDER BY d.weight ASC, m.short_name ASC, m.name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("query maps: %w", err)
	}
	defer rows.Close()
	items := make([]domain.MapResource, 0)
	for rows.Next() {
		var item domain.MapResource
		var workshopID sql.NullInt64
		if err := rows.Scan(&item.ID, &item.Name, &item.ShortName, &workshopID, &item.Difficulty, &item.Description); err != nil {
			return nil, fmt.Errorf("scan map: %w", err)
		}
		if workshopID.Valid {
			item.WorkshopID = strconv.FormatInt(workshopID.Int64, 10)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate maps: %w", err)
	}
	return items, nil
}

func (r *Repository) PlayerReadModel(ctx context.Context, steamID uint64) (domain.PlayerReadModel, error) {
	account, err := r.Account(ctx, steamID)
	if err != nil {
		return domain.PlayerReadModel{}, err
	}
	inventory, err := r.Inventory(ctx, steamID)
	if err != nil {
		return domain.PlayerReadModel{}, err
	}
	history, err := r.PurchaseHistory(ctx, steamID)
	if err != nil {
		return domain.PlayerReadModel{}, err
	}
	seasonPass, err := r.SeasonPass(ctx, steamID)
	if err != nil {
		return domain.PlayerReadModel{}, err
	}
	penalties, err := r.Penalties(ctx, steamID)
	if err != nil {
		return domain.PlayerReadModel{}, err
	}
	return domain.PlayerReadModel{Account: account, Inventory: inventory, PurchaseHistory: history, SeasonPass: seasonPass, Penalties: penalties}, nil
}

func (r *Repository) Account(ctx context.Context, steamID uint64) (domain.AccountOverview, error) {
	var name string
	var starlight, onlineTime int64
	err := r.db.QueryRowContext(ctx, `SELECT name, starlight, online_time FROM star_user WHERE steamid = ? LIMIT 1`, steamID).Scan(&name, &starlight, &onlineTime)
	if errors.Is(err, sql.ErrNoRows) {
		name = "StarCS 玩家"
		err = nil
	}
	if err != nil {
		return domain.AccountOverview{}, fmt.Errorf("query player account: %w", err)
	}
	return domain.AccountOverview{
		Profile: domain.Profile{
			UserID:         strconv.FormatUint(steamID, 10),
			DisplayName:    name,
			Verified:       true,
			PlayHours:      int(onlineTime / 3600),
			SteamConnected: true,
		},
		Wallet:        domain.Wallet{Starlight: starlight, StarlightAvailable: true},
		ExchangeRates: []domain.ExchangeRate{},
	}, nil
}

func (r *Repository) PurchaseHistory(ctx context.Context, steamID uint64) ([]domain.PurchaseHistoryItem, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, product_name, currency_type, quantity, days, total_price, state, description, created
		FROM sls_purchase_history
		WHERE steam_id = ?
		ORDER BY created DESC, id DESC
		LIMIT 30
	`, steamID)
	if err != nil {
		return nil, fmt.Errorf("query purchase history: %w", err)
	}
	defer rows.Close()
	items := make([]domain.PurchaseHistoryItem, 0)
	for rows.Next() {
		var item domain.PurchaseHistoryItem
		var created time.Time
		if err := rows.Scan(&item.ID, &item.ProductName, &item.CurrencyType, &item.Quantity, &item.Days, &item.TotalPrice, &item.State, &item.Description, &created); err != nil {
			return nil, fmt.Errorf("scan purchase history: %w", err)
		}
		item.CreatedAt = created.Format(time.RFC3339)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) SeasonPass(ctx context.Context, steamID uint64) (domain.SeasonPassOverview, error) {
	var item domain.SeasonPassOverview
	var claimedRewards string
	var updated time.Time
	err := r.db.QueryRowContext(ctx, `
		SELECT season_id, pass_type, level, experience, claimed_rewards, star_source_chest_opened, update_time
		FROM season_pass_players
		WHERE steam_id64 = ?
		ORDER BY season_id DESC
		LIMIT 1
	`, steamID).Scan(&item.SeasonID, &item.PassType, &item.Level, &item.Experience, &claimedRewards, &item.StarSourceChestOpened, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return item, nil
	}
	if err != nil {
		return item, fmt.Errorf("query season pass: %w", err)
	}
	item.Available = true
	item.ClaimedRewardCount = collectionCount(claimedRewards)
	item.UpdatedAt = updated.Format(time.RFC3339)
	_ = r.db.QueryRowContext(ctx, `SELECT games_completed, online_minutes FROM season_pass_daily_quests WHERE steam_id64 = ? ORDER BY quest_date DESC LIMIT 1`, steamID).Scan(&item.DailyGames, &item.DailyOnlineMinutes)
	var completedModes string
	_ = r.db.QueryRowContext(ctx, `SELECT games_completed, completed_modes FROM season_pass_weekly_quests WHERE steam_id64 = ? ORDER BY week_start_date DESC LIMIT 1`, steamID).Scan(&item.WeeklyGames, &completedModes)
	item.WeeklyCompletedModes = collectionCount(completedModes)
	return item, nil
}

func (r *Repository) Penalties(ctx context.Context, steamID uint64) ([]domain.AccountPenalty, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT '游戏封禁', reason, COALESCE(mode_name, ''), permanent, expire_date, created_date
		FROM star_ban
		WHERE player_steamid = ? AND penalty_state = 'ACTIVE'
		UNION ALL
		SELECT CASE ban_type WHEN 1 THEN '文字禁言' WHEN 2 THEN '语音禁言' ELSE '通讯限制' END,
		       reason, COALESCE(mode_name, ''), permanent,
		       CASE WHEN permanent = 1 THEN NULL ELSE DATE_ADD(updated_at, INTERVAL remain_time MINUTE) END,
		       created_date
		FROM star_ban_rtl
		WHERE player_steamid = ? AND penalty_state = 'ACTIVE'
		ORDER BY created_date DESC
	`, steamID, steamID)
	if err != nil {
		return nil, fmt.Errorf("query penalties: %w", err)
	}
	defer rows.Close()
	items := make([]domain.AccountPenalty, 0)
	for rows.Next() {
		var item domain.AccountPenalty
		var expires sql.NullTime
		var created time.Time
		if err := rows.Scan(&item.Type, &item.Reason, &item.Mode, &item.Permanent, &expires, &created); err != nil {
			return nil, fmt.Errorf("scan penalty: %w", err)
		}
		if expires.Valid {
			item.ExpiresAt = expires.Time.Format(time.RFC3339)
		}
		item.CreatedAt = created.Format(time.RFC3339)
		items = append(items, item)
	}
	return items, rows.Err()
}

func announcementSummary(raw string) string {
	var payload struct {
		Sections []struct {
			Title  string `json:"title"`
			Blocks []struct {
				Kind string `json:"kind"`
				Text string `json:"text"`
			} `json:"blocks"`
		} `json:"sections"`
	}
	if json.Unmarshal([]byte(raw), &payload) != nil {
		return "点击查看公告详情"
	}
	for _, section := range payload.Sections {
		for _, block := range section.Blocks {
			if text := strings.TrimSpace(block.Text); text != "" {
				return text
			}
		}
		if title := strings.TrimSpace(section.Title); title != "" {
			return title
		}
	}
	return "点击查看公告详情"
}

func publicFileURL(relativePath string) string {
	relativePath = strings.TrimSpace(relativePath)
	if relativePath == "" {
		return ""
	}
	if strings.HasPrefix(relativePath, "http://") || strings.HasPrefix(relativePath, "https://") {
		return relativePath
	}
	return "https://www.starcs.cn/" + strings.TrimLeft(relativePath, "/")
}

func collectionCount(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" || raw == "{}" {
		return 0
	}
	var array []any
	if json.Unmarshal([]byte(raw), &array) == nil {
		return len(array)
	}
	var object map[string]any
	if json.Unmarshal([]byte(raw), &object) == nil {
		return len(object)
	}
	return len(strings.FieldsFunc(raw, func(character rune) bool { return character == ',' || character == '#' }))
}

func displayType(label string, productType int) (string, string) {
	normalized := strings.ToLower(label)
	switch {
	case strings.Contains(normalized, "playermodel"), strings.Contains(normalized, "player_skin"), strings.Contains(normalized, "agent"):
		return "玩家外观", "user-round"
	case strings.Contains(normalized, "weapon"), strings.Contains(normalized, "weaponmodel"):
		return "武器外观", "zap"
	case productType == 2:
		return "消耗品", "package"
	default:
		return "物品", "gift"
	}
}

func displayRarity(id int, name string) string {
	if strings.TrimSpace(name) != "" {
		return name
	}
	switch id {
	case 1:
		return "R"
	case 2:
		return "SR"
	case 3:
		return "SSR"
	case 4:
		return "UR"
	case 5:
		return "Crystal"
	default:
		return "普通"
	}
}

func rarityTone(id int) string {
	switch id {
	case 5:
		return "from-cyan-400 to-blue-600"
	case 4:
		return "from-amber-400 to-orange-600"
	case 3:
		return "from-violet-500 to-fuchsia-600"
	case 2:
		return "from-primary to-secondary"
	case 1:
		return "from-emerald-500 to-cyan-500"
	default:
		return "from-slate-500 to-slate-700"
	}
}

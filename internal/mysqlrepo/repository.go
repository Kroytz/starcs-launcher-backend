package mysqlrepo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/starcs/star-launcher-backend/internal/domain"
)

var ErrInvalidCredentials = errors.New("invalid credentials")

type Repository struct {
	db                        *sql.DB
	challengeDB               *sql.DB
	challengeCatalogAvailable bool
}

func Open(ctx context.Context, dsn string) (*Repository, error) {
	db, err := openDatabase(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open primary database: %w", err)
	}
	return &Repository{db: db}, nil
}

func openDatabase(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func (r *Repository) ConnectChallenge(ctx context.Context, dsn string) error {
	db, err := openDatabase(ctx, dsn)
	if err != nil {
		return fmt.Errorf("open challenge database: %w", err)
	}
	if r.challengeDB != nil {
		_ = r.challengeDB.Close()
	}
	r.challengeDB = db
	err = db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = DATABASE() AND table_name = 'starduststore_catalog'
		)
	`).Scan(&r.challengeCatalogAvailable)
	if err != nil {
		_ = db.Close()
		r.challengeDB = nil
		return fmt.Errorf("inspect challenge catalog: %w", err)
	}
	return nil
}

func (r *Repository) ChallengeCatalogAvailable() bool {
	return r.challengeCatalogAvailable
}

func (r *Repository) Close() error {
	var challengeErr error
	if r.challengeDB != nil {
		challengeErr = r.challengeDB.Close()
	}
	return errors.Join(r.db.Close(), challengeErr)
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
			AND (i.expired IS NULL OR i.expired > NOW())
			AND (i.number > 0 OR i.expired > NOW())
			AND p.show_state IN (1, 3)
			AND LOWER(COALESCE(p.label, '')) NOT LIKE '%character%'
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
			Source:     "starlight",
			UniqueID:   strconv.FormatInt(productID, 10),
			Name:       name,
			Type:       itemType,
			Rarity:     displayRarity(rarityID, rarityName),
			Quantity:   inventoryQuantity(quantity),
			Icon:       icon,
			Tone:       rarityTone(rarityID),
			AcquiredAt: created.Format(time.RFC3339),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate inventory: %w", err)
	}
	if r.challengeDB != nil && r.challengeCatalogAvailable {
		challengeItems, err := r.challengeInventory(ctx, steamID)
		if err != nil {
			return nil, err
		}
		items = append(items, challengeItems...)
	}
	return items, nil
}

func (r *Repository) challengeInventory(ctx context.Context, steamID uint64) ([]domain.InventoryItem, error) {
	rows, err := r.challengeDB.QueryContext(ctx, `
		SELECT c.item_type, c.unique_id, c.display_name, c.category_name,
		       COUNT(*), MIN(i.DateOfPurchase)
		FROM starduststore_items AS i
		INNER JOIN starduststore_catalog AS c
		        ON BINARY c.item_type = BINARY i.Type
		       AND BINARY c.unique_id = BINARY i.UniqueId
		       AND c.enabled = 1
		       AND (c.restricted_steam_id IS NULL OR c.restricted_steam_id = i.SteamID)
		WHERE i.SteamID = ?
		  AND (i.DateOfExpiration IS NULL
		       OR i.DateOfExpiration < '1000-01-01 00:00:00'
		       OR i.DateOfExpiration > NOW())
		GROUP BY c.item_type, c.unique_id, c.display_name, c.category_name, c.sort_order
		ORDER BY c.sort_order, c.item_type, c.unique_id
	`, steamID)
	if err != nil {
		return nil, fmt.Errorf("query challenge inventory: %w", err)
	}
	defer rows.Close()

	items := make([]domain.InventoryItem, 0)
	for rows.Next() {
		var itemType, uniqueID, displayName, category string
		var quantity int64
		var acquiredAt time.Time
		if err := rows.Scan(&itemType, &uniqueID, &displayName, &category, &quantity, &acquiredAt); err != nil {
			return nil, fmt.Errorf("scan challenge inventory: %w", err)
		}
		_, icon := challengeCategory(itemType)
		items = append(items, domain.InventoryItem{
			ID:         "challenge-" + itemType + "-" + uniqueID,
			Source:     "stardust",
			UniqueID:   uniqueID,
			Name:       displayName,
			Type:       category,
			Rarity:     "星尘",
			Quantity:   quantity,
			Icon:       icon,
			Tone:       "from-secondary to-violet-600",
			AcquiredAt: acquiredAt.Format(time.RFC3339),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate challenge inventory: %w", err)
	}
	return items, nil
}

func (r *Repository) Announcements(ctx context.Context) ([]domain.Announcement, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT a.id, a.title, a.render_payload, a.type, a.published_at, a.created,
		       COALESCE(cover.relative_path, ''), COALESCE(detail.relative_path, '')
		FROM scs_announcement AS a
		LEFT JOIN scs_file AS cover ON cover.id = a.cover_image_id
		LEFT JOIN scs_file AS detail ON detail.id = a.detail_image_id
		WHERE a.status = 1
		  AND (a.start_at IS NULL OR a.start_at <= NOW())
		  AND (a.end_at IS NULL OR a.end_at >= NOW())
		ORDER BY a.is_pinned DESC, a.priority DESC, a.published_at DESC, a.id DESC
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
		var coverPath string
		var detailPath string
		if err := rows.Scan(&id, &title, &payload, &announcementType, &published, &created, &coverPath, &detailPath); err != nil {
			return nil, fmt.Errorf("scan announcement: %w", err)
		}
		publishedAt := created
		if published.Valid {
			publishedAt = published.Time
		}
		renderPayload, err := r.resolveAnnouncementPayload(ctx, payload.String)
		if err != nil {
			return nil, fmt.Errorf("resolve announcement %d payload: %w", id, err)
		}
		items = append(items, domain.Announcement{
			ID:             strconv.FormatUint(id, 10),
			Title:          title,
			Content:        announcementSummary(payload.String),
			Level:          map[bool]string{true: "event", false: "info"}[announcementType == 1],
			Dismissible:    true,
			DisplayDate:    publishedAt.Format("01 / 02"),
			PublishedAt:    publishedAt.Format(time.RFC3339),
			CoverImageURL:  publicFileURL(coverPath),
			DetailImageURL: publicFileURL(detailPath),
			RenderPayload:  renderPayload,
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
		category, icon := displayType(label, productType)
		items = append(items, domain.StoreItem{
			ID:              fmt.Sprintf("pricing-%d", pricingID),
			ExternalID:      strconv.FormatInt(productID, 10),
			Currency:        "starlight",
			Category:        category,
			PurchaseBackend: "star-product",
			Title:           name,
			Description:     description,
			Price:           int64(price),
			Icon:            icon,
			Tone:            rarityTone(rarityID),
			Tag:             displayRarity(rarityID, rarityName),
			Enabled:         true,
			Sort:            sortOrder,
			ImageURL:        publicFileURL(relativePath),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate store items: %w", err)
	}
	afdianItems, err := r.afdianStoreItems(ctx)
	if err != nil {
		return nil, err
	}
	items = append(items, afdianItems...)
	if r.challengeDB != nil && r.challengeCatalogAvailable {
		challengeItems, err := r.challengeStoreItems(ctx)
		if err != nil {
			return nil, err
		}
		items = append(items, challengeItems...)
	}
	return items, nil
}

func (r *Repository) afdianStoreItems(ctx context.Context) ([]domain.StoreItem, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT p.id, p.afdian_plan_id, p.name, p.prefab_type, COALESCE(p.desc, ''),
		       COALESCE(p.label, ''), p.price
		FROM afdian_cdk_product AS p
		WHERE p.state = 1 AND TRIM(p.afdian_plan_id) <> ''
		ORDER BY p.id
	`)
	if err != nil {
		return nil, fmt.Errorf("query afdian store items: %w", err)
	}
	defer rows.Close()

	items := make([]domain.StoreItem, 0)
	for rows.Next() {
		var id int64
		var planID, name, description, label string
		var prefabType int
		var price int64
		if err := rows.Scan(&id, &planID, &name, &prefabType, &description, &label, &price); err != nil {
			return nil, fmt.Errorf("scan afdian store item: %w", err)
		}
		category, icon := afdianCategory(label, prefabType)
		items = append(items, domain.StoreItem{
			ID:              fmt.Sprintf("afdian-%d", id),
			ExternalID:      strings.TrimSpace(planID),
			Currency:        "afdian",
			Category:        category,
			PurchaseBackend: "afdian-cdk",
			PurchaseURL:     afdianPurchaseURL(planID),
			Title:           name,
			Description:     description,
			Price:           price,
			Icon:            icon,
			Tone:            "from-pink-500 to-rose-600",
			Tag:             category,
			Enabled:         true,
			Sort:            int(id),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate afdian store items: %w", err)
	}
	return items, nil
}

func (r *Repository) challengeStoreItems(ctx context.Context) ([]domain.StoreItem, error) {
	rows, err := r.challengeDB.QueryContext(ctx, `
		SELECT item_type, unique_id, category_name, display_name, price, sort_order
		FROM starduststore_catalog
		WHERE enabled = 1 AND purchasable = 1 AND hidden = 0
		ORDER BY sort_order, category_name, display_name
	`)
	if err != nil {
		return nil, fmt.Errorf("query challenge store items: %w", err)
	}
	defer rows.Close()

	items := make([]domain.StoreItem, 0)
	for rows.Next() {
		var itemType, uniqueID, category, displayName string
		var price int64
		var sortOrder int
		if err := rows.Scan(&itemType, &uniqueID, &category, &displayName, &price, &sortOrder); err != nil {
			return nil, fmt.Errorf("scan challenge store item: %w", err)
		}
		_, icon := challengeCategory(itemType)
		items = append(items, domain.StoreItem{
			ID:              "challenge-" + itemType + "-" + uniqueID,
			ExternalID:      uniqueID,
			Currency:        "stardust",
			Category:        category,
			PurchaseBackend: "challenge-stardust",
			Title:           displayName,
			Description:     "来自 DB_CHALLENGE 商品目录；购买将使用独立的星尘商店流程。",
			Price:           price,
			Icon:            icon,
			Tone:            "from-secondary to-violet-600",
			Tag:             category,
			Enabled:         true,
			Sort:            sortOrder,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate challenge store items: %w", err)
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
	wallet := domain.Wallet{Starlight: starlight, StarlightAvailable: true}
	if r.challengeDB != nil {
		var challengeName string
		err := r.challengeDB.QueryRowContext(ctx, `
			SELECT PlayerName, StarDust
			FROM starduststore_players
			WHERE SteamID = ?
			ORDER BY id DESC
			LIMIT 1
		`, steamID).Scan(&challengeName, &wallet.Stardust)
		if errors.Is(err, sql.ErrNoRows) {
			err = nil
		}
		if err != nil {
			return domain.AccountOverview{}, fmt.Errorf("query challenge player account: %w", err)
		}
		wallet.StardustAvailable = true
		if name == "StarCS 玩家" && strings.TrimSpace(challengeName) != "" {
			name = challengeName
		}
	}
	return domain.AccountOverview{
		Profile: domain.Profile{
			UserID:         strconv.FormatUint(steamID, 10),
			DisplayName:    name,
			Verified:       true,
			PlayHours:      int(onlineTime / 3600),
			SteamConnected: true,
		},
		Wallet:        wallet,
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

func announcementPayload(raw string) json.RawMessage {
	raw = strings.TrimSpace(raw)
	if raw == "" || !json.Valid([]byte(raw)) {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(raw)
}

func (r *Repository) resolveAnnouncementPayload(ctx context.Context, raw string) (json.RawMessage, error) {
	payload := announcementPayload(raw)
	var document map[string]any
	if err := json.Unmarshal(payload, &document); err != nil {
		return json.RawMessage(`{}`), nil
	}
	sections, _ := document["sections"].([]any)
	for _, rawSection := range sections {
		section, _ := rawSection.(map[string]any)
		blocks, _ := section["blocks"].([]any)
		for _, rawBlock := range blocks {
			block, _ := rawBlock.(map[string]any)
			imageID, ok := block["imageId"].(float64)
			if !ok || imageID <= 0 {
				continue
			}
			var relativePath string
			err := r.db.QueryRowContext(ctx, `SELECT COALESCE(relative_path, '') FROM scs_file WHERE id = ? LIMIT 1`, uint64(imageID)).Scan(&relativePath)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if imageURL := publicFileURL(relativePath); imageURL != "" {
				block["imageUrl"] = imageURL
			}
		}
	}
	resolved, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	return resolved, nil
}

func publicFileURL(relativePath string) string {
	relativePath = strings.TrimSpace(relativePath)
	if relativePath == "" {
		return ""
	}
	if strings.HasPrefix(relativePath, "http://") || strings.HasPrefix(relativePath, "https://") {
		return relativePath
	}
	return "https://static.starcs.cn/" + strings.TrimLeft(relativePath, "/")
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
	case strings.Contains(normalized, "itemcard"):
		return "道具卡", "package"
	case strings.Contains(normalized, "character"):
		return "集字", "trophy"
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

func inventoryQuantity(quantity int64) int64 {
	if quantity > 0 {
		return quantity
	}
	return 1
}

func afdianCategory(label string, prefabType int) (string, string) {
	normalized := strings.ToLower(label)
	switch {
	case prefabType == 1, strings.Contains(normalized, "card"):
		return "道具卡", "package"
	case strings.Contains(normalized, "vip"):
		return "会员", "star"
	case strings.Contains(normalized, "starlight"):
		return "星光", "sparkles"
	case strings.Contains(normalized, "weapon"):
		return "武器外观", "zap"
	case strings.Contains(normalized, "packet"):
		return "礼包", "gift"
	default:
		return "其他", "shopping-bag"
	}
}

func afdianPurchaseURL(planID string) string {
	planID = strings.TrimSpace(planID)
	if planID == "" {
		return ""
	}
	return "https://www.ifdian.net/item/" + url.PathEscape(planID)
}

func challengeCategory(itemType string) (string, string) {
	switch strings.ToLower(strings.TrimSpace(itemType)) {
	case "chatcolor":
		return "聊天颜色", "sparkles"
	case "namecolor":
		return "名称颜色", "user-round"
	case "chattag":
		return "聊天称号", "trophy"
	case "cheer":
		return "欢呼语音", "gift"
	case "death_voice":
		return "死亡语音", "package"
	case "grenade_voice":
		return "投掷物语音", "zap"
	case "hurt_voice":
		return "受伤语音", "shield-check"
	case "respawn_voice":
		return "重生语音", "star"
	case "playerskincard":
		return "人物皮肤卡", "user-round"
	case "directionalhammer":
		return "定向强化核心", "zap"
	default:
		return "星尘物品", "gem"
	}
}

func challengeItemTitle(itemType, uniqueID string) string {
	category, _ := challengeCategory(itemType)
	variant := strings.TrimSpace(uniqueID)
	normalizedType := strings.ToLower(strings.TrimSpace(itemType))
	normalizedVariant := strings.ToLower(variant)
	for _, prefix := range []string{normalizedType, strings.ReplaceAll(normalizedType, "_", "")} {
		if strings.HasPrefix(normalizedVariant, prefix) {
			variant = variant[len(prefix):]
			normalizedVariant = normalizedVariant[len(prefix):]
			break
		}
	}
	variant = strings.Trim(variant, "_- ")
	if translated, ok := challengeVariantNames[strings.ToLower(variant)]; ok {
		variant = translated
	} else {
		variant = strings.ReplaceAll(variant, "_", " ")
	}
	if variant == "" {
		return category
	}
	return category + " · " + variant
}

var challengeVariantNames = map[string]string{
	"blue":        "蓝色",
	"bluegrey":    "蓝灰色",
	"darkblue":    "深蓝色",
	"darkred":     "深红色",
	"gold":        "金色",
	"green":       "绿色",
	"grey":        "灰色",
	"lightblue":   "浅蓝色",
	"lightred":    "浅红色",
	"lightyellow": "浅黄色",
	"lime":        "青柠色",
	"magenta":     "洋红色",
	"olive":       "橄榄色",
	"orange":      "橙色",
	"purple":      "紫色",
	"red":         "红色",
	"silver":      "银色",
	"white":       "白色",
	"yellow":      "黄色",
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

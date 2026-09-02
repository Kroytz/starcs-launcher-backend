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
	"github.com/starcs/star-launcher-backend/internal/passwordauth"
)

var (
	ErrInvalidCredentials    = errors.New("invalid credentials")
	ErrPlayerNotFound        = errors.New("player not found")
	ErrPricingNotFound       = errors.New("pricing not found")
	ErrInsufficientStarlight = errors.New("insufficient starlight")
	ErrInsufficientStardust  = errors.New("insufficient stardust")
	ErrProductAlreadyOwned   = errors.New("product already owned")
	ErrPermanentVersionOwned = errors.New("permanent version already owned")
	ErrStardustItemNotOwned  = errors.New("stardust item not owned")
	ErrChallengeDBUnavailable = errors.New("challenge database unavailable")
)

// starlightStoreProductFilter mirrors StarLightStore CanShowInStore() and the
// launcher store catalog: only AllShow(1)/OnlyStoreShow(2), no exclusive
// type=3 products, and no StarForge pool items.
const starlightStoreProductFilter = `
		  AND p.show_state IN (1, 2)
		  AND p.type != 3
		  AND LOWER(COALESCE(p.label, '')) NOT LIKE '%starforge%'`

type Repository struct {
	db                        *sql.DB
	challengeDB               *sql.DB
	challengeCatalogAvailable bool
	groupMembership           GroupMembershipChecker
	announcementCache         publicCatalogEntry[[]domain.Announcement]
	storeItemCache            publicCatalogEntry[[]domain.StoreItem]
	mapCache                  publicCatalogEntry[[]domain.MapResource]
}

// GroupMembershipChecker resolves the Steam Community group entitlement used
// by StarLightStore products with use_limit=4.
type GroupMembershipChecker interface {
	IsMember(ctx context.Context, groupID, steamID uint64, maxMembers int) bool
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

func (r *Repository) SetGroupMembershipChecker(checker GroupMembershipChecker) {
	r.groupMembership = checker
}

func (r *Repository) Close() error {
	var challengeErr error
	if r.challengeDB != nil {
		challengeErr = r.challengeDB.Close()
	}
	return errors.Join(r.db.Close(), challengeErr)
}

func (r *Repository) Authenticate(ctx context.Context, steamID uint64, password string) error {
	encoded, err := r.GamePasswordHash(ctx, steamID)
	if err != nil {
		if errors.Is(err, ErrPlayerNotFound) {
			return ErrInvalidCredentials
		}
		return err
	}
	if encoded == "" {
		return ErrInvalidCredentials
	}
	valid, err := passwordauth.Verify(ctx, password, encoded)
	if err != nil {
		return fmt.Errorf("verify game password hash: %w", err)
	}
	if !valid {
		return ErrInvalidCredentials
	}
	return nil
}

func (r *Repository) GamePasswordHash(ctx context.Context, steamID uint64) (string, error) {
	var encoded sql.NullString
	err := r.db.QueryRowContext(ctx, `
		SELECT game_password_hash
		FROM star_user
		WHERE steamid = ?
		LIMIT 1
	`, steamID).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrPlayerNotFound
	}
	if err != nil {
		return "", fmt.Errorf("query game password hash: %w", err)
	}
	return encoded.String, nil
}

func (r *Repository) UpdateGamePasswordHash(ctx context.Context, steamID uint64, encoded string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE star_user
		SET game_password_hash = ?, game_password_updated_at = NOW(6)
		WHERE steamid = ?
	`, encoded, steamID)
	if err != nil {
		return fmt.Errorf("update game password hash: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read game password update result: %w", err)
	}
	if rows == 0 {
		return ErrPlayerNotFound
	}
	return nil
}

// Inventory 返回玩家当前有效库存。非数量型永久物品兼容 expired IS NULL（旧数据）与 '9000-01-01' 哨兵，
// 与 StoreItemsForPlayer / PurchaseStarlight 的「已拥有永久版」判定保持同一口径。
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
			i.created,
			i.expired,
			COALESCE(p.desc, ''),
			COALESCE(NULLIF(TRIM(p.mode), ''), 'ALL'),
			p.use_limit,
			COALESCE(p.use_limit_info, ''),
			COALESCE(wm.prefab, ''),
			COALESCE(wm.weapon_type, 0)
		FROM sls_player_inventory AS i
		INNER JOIN sls_product AS p ON p.id = i.product_id
		LEFT JOIN sls_product_rarity AS r ON r.id = p.rarity_id
		LEFT JOIN sls_product_detail_weapon_model AS wm ON wm.product_id = p.id
		WHERE i.steamid = ?
			AND (
				-- 数量型：仅展示仍有库存的行
				(p.type = 2 AND i.number > 0)
				OR (
					-- 非数量型：兼容旧永久行 expired IS NULL、插件哨兵 9000-01-01，以及未过期期限档
					p.type != 2
					AND (
						i.expired IS NULL
						OR i.expired >= '9000-01-01 00:00:00'
						OR i.expired > NOW()
					)
				)
			)
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
			productID    int64
			name         string
			label        string
			productType  int
			rarityID     int
			rarityName   string
			quantity     int64
			created      time.Time
			expired      sql.NullTime
			description  string
			mode         string
			useLimit     int
			useLimitInfo string
			weaponPrefab string
			weaponTypeID int
		)
		if err := rows.Scan(&productID, &name, &label, &productType, &rarityID, &rarityName, &quantity, &created, &expired, &description, &mode, &useLimit, &useLimitInfo, &weaponPrefab, &weaponTypeID); err != nil {
			return nil, fmt.Errorf("scan inventory: %w", err)
		}
		itemType, icon := displayType(label, productType)
		items = append(items, domain.InventoryItem{
			ProductID:    productID,
			ID:           fmt.Sprintf("product-%d", productID),
			Source:       "starlight",
			UniqueID:     strconv.FormatInt(productID, 10),
			Name:         name,
			Type:         itemType,
			Rarity:       displayRarity(rarityID, rarityName),
			Quantity:     inventoryQuantity(quantity),
			Icon:         icon,
			Tone:         rarityTone(rarityID),
			AcquiredAt:   created.Format(time.RFC3339),
			ExpiresAt:    formatExpiry(expired),
			Description:  description,
			Mode:         mode,
			UseLimit:     useLimit,
			UseLimitInfo: useLimitInfo,
			WeaponPrefab: weaponPrefab,
			WeaponType:   weaponModelTypeName(weaponTypeID),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate inventory: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close inventory rows: %w", err)
	}

	exclusiveItems, err := r.exclusiveInventory(ctx, steamID, items)
	if err != nil {
		return nil, err
	}
	items = append(items, exclusiveItems...)
	items = r.filterExclusiveInventory(ctx, steamID, items)

	if r.challengeDB != nil && r.challengeCatalogAvailable {
		challengeItems, err := r.challengeInventory(ctx, steamID)
		if err != nil {
			return nil, err
		}
		items = append(items, challengeItems...)
	}
	return items, nil
}

// exclusiveInventory mirrors StarLightStore's temporary inventory behavior for
// personal (use_limit=8) and Steam group (use_limit=4) products. These type=3
// products do not normally have rows in sls_player_inventory.
func (r *Repository) exclusiveInventory(ctx context.Context, steamID uint64, existing []domain.InventoryItem) ([]domain.InventoryItem, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT
			p.id,
			p.name,
			p.label,
			p.type,
			p.rarity_id,
			COALESCE(r.name, ''),
			p.created,
			COALESCE(p.desc, ''),
			COALESCE(NULLIF(TRIM(p.mode), ''), 'ALL'),
			p.use_limit,
			COALESCE(p.use_limit_info, ''),
			COALESCE(wm.prefab, ''),
			COALESCE(wm.weapon_type, 0)
		FROM sls_product AS p
		LEFT JOIN sls_product_rarity AS r ON r.id = p.rarity_id
		LEFT JOIN sls_product_detail_weapon_model AS wm ON wm.product_id = p.id
		WHERE p.type = 3
			AND p.use_limit IN (4, 8)
			AND p.show_state IN (1, 3)
			AND LOWER(COALESCE(p.label, '')) NOT LIKE '%character%'
			AND (p.use_limit = 4 OR TRIM(p.use_limit_info) = ?)
		ORDER BY p.rarity_id DESC, p.updated DESC, p.id ASC
	`, strconv.FormatUint(steamID, 10))
	if err != nil {
		return nil, fmt.Errorf("query exclusive inventory: %w", err)
	}
	defer rows.Close()

	existingIDs := make(map[int64]struct{}, len(existing))
	for _, item := range existing {
		existingIDs[item.ProductID] = struct{}{}
	}
	items := make([]domain.InventoryItem, 0)
	for rows.Next() {
		var (
			productID    int64
			name         string
			label        string
			productType  int
			rarityID     int
			rarityName   string
			created      time.Time
			description  string
			mode         string
			useLimit     int
			useLimitInfo string
			weaponPrefab string
			weaponTypeID int
		)
		if err := rows.Scan(&productID, &name, &label, &productType, &rarityID, &rarityName, &created, &description, &mode, &useLimit, &useLimitInfo, &weaponPrefab, &weaponTypeID); err != nil {
			return nil, fmt.Errorf("scan exclusive inventory: %w", err)
		}
		if _, exists := existingIDs[productID]; exists {
			continue
		}
		existingIDs[productID] = struct{}{}
		itemType, icon := displayType(label, productType)
		items = append(items, domain.InventoryItem{
			ProductID:    productID,
			ID:           fmt.Sprintf("product-%d", productID),
			Source:       "starlight",
			UniqueID:     strconv.FormatInt(productID, 10),
			Name:         name,
			Type:         itemType,
			Rarity:       displayRarity(rarityID, rarityName),
			Quantity:     1,
			Icon:         icon,
			Tone:         rarityTone(rarityID),
			AcquiredAt:   created.Format(time.RFC3339),
			Description:  description,
			Mode:         mode,
			UseLimit:     useLimit,
			UseLimitInfo: useLimitInfo,
			WeaponPrefab: weaponPrefab,
			WeaponType:   weaponModelTypeName(weaponTypeID),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate exclusive inventory: %w", err)
	}
	return items, nil
}

func (r *Repository) filterExclusiveInventory(ctx context.Context, steamID uint64, items []domain.InventoryItem) []domain.InventoryItem {
	// Steam Community is an optional entitlement source. Keep one short budget
	// for all groups so an unreachable service can never consume the launcher's
	// entire login timeout or cancel the subsequent database reads.
	groupCtx, cancelGroupChecks := context.WithTimeout(ctx, 2*time.Second)
	defer cancelGroupChecks()

	allowed := make([]bool, len(items))
	type groupCheck struct {
		index      int
		groupID    uint64
		maxMembers int
	}
	checks := make([]groupCheck, 0)
	for index, item := range items {
		switch item.UseLimit {
		case 4:
			groupID, maxMembers, ok := parseSteamGroupLimit(item.UseLimitInfo)
			if ok && r.groupMembership != nil {
				checks = append(checks, groupCheck{index: index, groupID: groupID, maxMembers: maxMembers})
			}
		case 8:
			allowed[index] = personalExclusiveAllowed(steamID, item.UseLimitInfo)
		default:
			allowed[index] = true
		}
	}

	type groupResult struct {
		index   int
		allowed bool
	}
	results := make(chan groupResult, len(checks))
	semaphore := make(chan struct{}, 8)
	for _, check := range checks {
		check := check
		go func() {
			semaphore <- struct{}{}
			isMember := r.groupMembership.IsMember(groupCtx, check.groupID, steamID, check.maxMembers)
			<-semaphore
			results <- groupResult{index: check.index, allowed: isMember}
		}()
	}
	for range checks {
		result := <-results
		allowed[result.index] = result.allowed
	}

	filtered := make([]domain.InventoryItem, 0, len(items))
	for index, item := range items {
		if allowed[index] {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func personalExclusiveAllowed(steamID uint64, raw string) bool {
	boundSteamID, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
	return err == nil && boundSteamID != 0 && boundSteamID == steamID
}

func parseSteamGroupLimit(raw string) (uint64, int, bool) {
	parts := strings.Split(raw, ",")
	if len(parts) != 2 {
		return 0, 0, false
	}
	groupID, err := strconv.ParseUint(strings.TrimSpace(parts[0]), 10, 64)
	if err != nil || groupID == 0 {
		return 0, 0, false
	}
	maxMembers, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || maxMembers <= 0 {
		return 0, 0, false
	}
	return groupID, maxMembers, true
}

func weaponModelTypeName(value int) string {
	return map[int]string{
		1:  "Knife",
		2:  "CommonPistol",
		3:  "SilencerPistol",
		4:  "SubMachineGun",
		5:  "SniperRifle",
		6:  "MachineGun",
		7:  "Grenade",
		8:  "TubeFedShotGun",
		9:  "MagazineFedShotGun",
		10: "CommonRifle",
		11: "SilencerRifle",
		12: "SightRifle",
		13: "DoublePistol",
		14: "StackableItem",
	}[value]
}

func (r *Repository) challengeInventory(ctx context.Context, steamID uint64) ([]domain.InventoryItem, error) {
	rows, err := r.challengeDB.QueryContext(ctx, `
		SELECT c.item_type, c.unique_id, c.display_name, c.category_name,
		       COUNT(*), MIN(i.DateOfPurchase),
		       MAX(CASE WHEN i.DateOfExpiration < '1000-01-01 00:00:00' THEN NULL ELSE i.DateOfExpiration END),
		       e.UniqueId IS NOT NULL
		FROM starduststore_items AS i
		INNER JOIN starduststore_catalog AS c
		        ON BINARY c.item_type = BINARY i.Type
		       AND BINARY c.unique_id = BINARY i.UniqueId
		       AND c.enabled = 1
		       AND (c.restricted_steam_id IS NULL OR c.restricted_steam_id = i.SteamID)
		LEFT JOIN starduststore_equipments AS e
		       ON e.SteamID = i.SteamID
		      AND BINARY e.Type = BINARY c.item_type
		      AND BINARY e.UniqueId = BINARY c.unique_id
		WHERE i.SteamID = ?
		  AND (i.DateOfExpiration IS NULL
		       OR i.DateOfExpiration < '1000-01-01 00:00:00'
		       OR i.DateOfExpiration > NOW())
		GROUP BY c.item_type, c.unique_id, c.display_name, c.category_name, c.sort_order, e.UniqueId
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
		var expired sql.NullTime
		var equipped bool
		if err := rows.Scan(&itemType, &uniqueID, &displayName, &category, &quantity, &acquiredAt, &expired, &equipped); err != nil {
			return nil, fmt.Errorf("scan challenge inventory: %w", err)
		}
		_, icon := challengeCategory(itemType)
		items = append(items, domain.InventoryItem{
			ID:           "challenge-" + itemType + "-" + uniqueID,
			Source:       "stardust",
			UniqueID:     uniqueID,
			Name:         displayName,
			Type:         category,
			Rarity:       "星尘",
			Quantity:     quantity,
			Icon:         icon,
			Tone:         "from-secondary to-violet-600",
			AcquiredAt:   acquiredAt.Format(time.RFC3339),
			ExpiresAt:    formatExpiry(expired),
			Equipped:     equipped,
			StardustType: itemType,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate challenge inventory: %w", err)
	}
	return items, nil
}

// StardustEquipments 返回该玩家当前已装备的全部星尘物品。
func (r *Repository) StardustEquipments(ctx context.Context, steamID uint64) ([]domain.StardustEquipment, error) {
	if r.challengeDB == nil {
		return nil, errors.New("challenge database is not configured")
	}
	rows, err := r.challengeDB.QueryContext(ctx, `
		SELECT Type, UniqueId, COALESCE(Slot, 0)
		FROM starduststore_equipments
		WHERE SteamID = ?
		ORDER BY Type, UniqueId
	`, steamID)
	if err != nil {
		return nil, fmt.Errorf("query stardust equipments: %w", err)
	}
	defer rows.Close()

	equipments := make([]domain.StardustEquipment, 0)
	for rows.Next() {
		var equipment domain.StardustEquipment
		if err := rows.Scan(&equipment.Type, &equipment.UniqueID, &equipment.Slot); err != nil {
			return nil, fmt.Errorf("scan stardust equipment: %w", err)
		}
		equipments = append(equipments, equipment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate stardust equipments: %w", err)
	}
	return equipments, nil
}

// EquipStardust 装备一件星尘物品；同一 Type 互斥，先删除同 Type 旧装备再写入新装备。
func (r *Repository) EquipStardust(ctx context.Context, steamID uint64, itemType, uniqueID string) error {
	if r.challengeDB == nil {
		return ErrChallengeDBUnavailable
	}
	var slot int
	err := r.challengeDB.QueryRowContext(ctx, `
		SELECT c.slot
		FROM starduststore_items AS i
		INNER JOIN starduststore_catalog AS c
		        ON BINARY c.item_type = BINARY i.Type
		       AND BINARY c.unique_id = BINARY i.UniqueId
		       AND c.enabled = 1
		       AND (c.restricted_steam_id IS NULL OR c.restricted_steam_id = i.SteamID)
		WHERE i.SteamID = ?
		  AND BINARY i.Type = BINARY ?
		  AND BINARY i.UniqueId = BINARY ?
		  AND (i.DateOfExpiration IS NULL
		       OR i.DateOfExpiration < '1000-01-01 00:00:00'
		       OR i.DateOfExpiration > NOW())
		LIMIT 1
	`, steamID, itemType, uniqueID).Scan(&slot)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrStardustItemNotOwned
	}
	if err != nil {
		return fmt.Errorf("validate stardust item ownership: %w", err)
	}

	tx, err := r.challengeDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin stardust equipment transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM starduststore_equipments WHERE SteamID = ? AND BINARY Type = BINARY ?`, steamID, itemType); err != nil {
		return fmt.Errorf("clear stardust equipment type: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO starduststore_equipments (SteamID, Type, UniqueId, Slot) VALUES (?, ?, ?, ?)`, steamID, itemType, uniqueID, slot); err != nil {
		return fmt.Errorf("insert stardust equipment: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit stardust equipment: %w", err)
	}
	return nil
}

// UnequipStardust 卸下一件星尘物品。
func (r *Repository) UnequipStardust(ctx context.Context, steamID uint64, itemType, uniqueID string) error {
	if r.challengeDB == nil {
		return ErrChallengeDBUnavailable
	}
	if _, err := r.challengeDB.ExecContext(ctx, `DELETE FROM starduststore_equipments WHERE SteamID = ? AND BINARY Type = BINARY ? AND BINARY UniqueId = BINARY ?`, steamID, itemType, uniqueID); err != nil {
		return fmt.Errorf("delete stardust equipment: %w", err)
	}
	return nil
}

// PurchaseStarlight 用星光购买一个星光商城价格档位：校验档位与余额，事务内扣星光、发库存、写购买历史，返回购买后的星光余额。
// 与 StarLightStore 插件行为对齐：永久物品的 expired 写 '9000-01-01' 哨兵；期限型重复购买时未过期则累加时长、已过期则从现在重新计算；
// 已持有永久版本（expired 为 NULL 或 >= '9000-01-01'）时永久档与期限档都拒绝重复购买；限定型（type=3）不可购买。
func (r *Repository) PurchaseStarlight(ctx context.Context, steamID uint64, pricingID int64) (int64, error) {
	var (
		productID   int64
		price       int64
		days        int
		quantity    int
		productType int
		productName string
	)
	// 可售性：价格档位 state + 与商城列表一致的商品可见性（show_state / type / label）
	err := r.db.QueryRowContext(ctx, `
		SELECT pp.product_id, pp.price, COALESCE(pp.days, 0), COALESCE(pp.quantity, 1), p.type, p.name
		FROM sls_product_pricing AS pp
		INNER JOIN sls_product AS p ON p.id = pp.product_id
		WHERE pp.id = ? AND pp.state = 1 AND pp.currency_id = 1`+starlightStoreProductFilter+`
		LIMIT 1
	`, pricingID).Scan(&productID, &price, &days, &quantity, &productType, &productName)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrPricingNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("query starlight pricing: %w", err)
	}
	// 启动器暂不允许 0 元购（插件允许，这里主动收紧）
	if price <= 0 || (productType == 2 && quantity <= 0) {
		return 0, ErrPricingNotFound
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin starlight purchase transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var balance int64
	err = tx.QueryRowContext(ctx, `SELECT starlight FROM star_user WHERE steamid = ? LIMIT 1 FOR UPDATE`, steamID).Scan(&balance)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrPlayerNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("lock starlight balance: %w", err)
	}
	if balance < price {
		return 0, ErrInsufficientStarlight
	}

	permanent := productType != 2 && days <= 0
	var permanentOwned bool
	err = tx.QueryRowContext(ctx, `
		SELECT (expired IS NULL OR expired >= '9000-01-01 00:00:00')
		FROM sls_player_inventory
		WHERE steamid = ? AND product_id = ?
		LIMIT 1 FOR UPDATE
	`, steamID, productID).Scan(&permanentOwned)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("query existing inventory entry: %w", err)
	}
	if err == nil && productType != 2 && permanentOwned {
		if permanent {
			return 0, ErrProductAlreadyOwned
		}
		return 0, ErrPermanentVersionOwned
	}

	if _, err := tx.ExecContext(ctx, `UPDATE star_user SET starlight = starlight - ? WHERE steamid = ?`, price, steamID); err != nil {
		return 0, fmt.Errorf("deduct starlight: %w", err)
	}

	switch {
	case productType == 2:
		// 数量型：number 累加，expired 恒为 NULL
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO sls_player_inventory (steamid, product_id, number, expired, updated, created)
			VALUES (?, ?, ?, NULL, NOW(), NOW())
			ON DUPLICATE KEY UPDATE number = number + VALUES(number), updated = NOW()
		`, steamID, productID, quantity); err != nil {
			return 0, fmt.Errorf("grant quantity product: %w", err)
		}
	case permanent:
		// 永久档：expired 写 '9000-01-01' 哨兵（与插件 SetPermanent 一致）；过期后重买时重置 created
		// 注意 ON DUPLICATE KEY UPDATE 从左到右求值，created 的判定必须排在 expired 赋值之前才能读到旧值
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO sls_player_inventory (steamid, product_id, number, expired, updated, created)
			VALUES (?, ?, 0, '9000-01-01 00:00:00', NOW(), NOW())
			ON DUPLICATE KEY UPDATE
				created = IF(expired IS NOT NULL AND expired < NOW(), NOW(), created),
				expired = '9000-01-01 00:00:00',
				updated = NOW()
		`, steamID, productID); err != nil {
			return 0, fmt.Errorf("grant permanent product: %w", err)
		}
	default:
		// 期限型：未过期在原到期时间上累加，已过期从现在重新计算并重置 created（与插件删除重建等效）
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO sls_player_inventory (steamid, product_id, number, expired, updated, created)
			VALUES (?, ?, 0, DATE_ADD(NOW(), INTERVAL ? DAY), NOW(), NOW())
			ON DUPLICATE KEY UPDATE
				created = IF(expired IS NOT NULL AND expired < NOW(), NOW(), created),
				expired = DATE_ADD(GREATEST(COALESCE(expired, NOW()), NOW()), INTERVAL ? DAY),
				updated = NOW()
		`, steamID, productID, days, days); err != nil {
			return 0, fmt.Errorf("grant timed product: %w", err)
		}
	}

	historyDays := -1
	historyQuantity := 1
	switch {
	case productType == 2:
		historyQuantity = quantity
	case productType == 1 && days > 0:
		historyDays = days
	default:
		historyDays = 0
	}
	balanceAfter := balance - price
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sls_purchase_history (steam_id, product_id, product_name, currency_type, quantity, days, total_price, balance_before, balance_after, state, description, created)
		VALUES (?, ?, ?, '星光', ?, ?, ?, ?, ?, 1, '', NOW())
	`, steamID, productID, productName, historyQuantity, historyDays, price, balance, balanceAfter); err != nil {
		return 0, fmt.Errorf("record purchase history: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit starlight purchase: %w", err)
	}
	return balanceAfter, nil
}

// PurchaseStardust 用星尘购买一件星尘商店物品：校验目录与余额，事务内扣星尘、写入物品，返回购买后的星尘余额。
// 星尘物品每件独立成行、永久持有（到期时间写 '0001-01-01' 哨兵），同 Type+UniqueId 已持有时拒绝重复购买。
func (r *Repository) PurchaseStardust(ctx context.Context, steamID uint64, itemType, uniqueID string) (int64, error) {
	if r.challengeDB == nil {
		return 0, errors.New("challenge database is not configured")
	}
	var price int64
	err := r.challengeDB.QueryRowContext(ctx, `
		SELECT price
		FROM starduststore_catalog
		WHERE BINARY item_type = BINARY ?
		  AND BINARY unique_id = BINARY ?
		  AND enabled = 1 AND purchasable = 1 AND hidden = 0
		  AND (restricted_steam_id IS NULL OR restricted_steam_id = ?)
		LIMIT 1
	`, itemType, uniqueID, steamID).Scan(&price)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrPricingNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("query stardust catalog item: %w", err)
	}
	// 启动器暂不允许 0 元购
	if price <= 0 {
		return 0, ErrPricingNotFound
	}

	tx, err := r.challengeDB.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin stardust purchase transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// 已持有同 Type+UniqueId 的有效物品时拒绝重复购买
	var owned bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM starduststore_items
			WHERE SteamID = ?
			  AND BINARY Type = BINARY ?
			  AND BINARY UniqueId = BINARY ?
			  AND (DateOfExpiration IS NULL
			       OR DateOfExpiration < '1000-01-01 00:00:00'
			       OR DateOfExpiration > NOW())
		)
	`, steamID, itemType, uniqueID).Scan(&owned); err != nil {
		return 0, fmt.Errorf("check stardust item ownership: %w", err)
	}
	if owned {
		return 0, ErrProductAlreadyOwned
	}

	// 从未进入挑战服的玩家没有记录，视为余额 0
	var balance int64
	playerExists := true
	err = tx.QueryRowContext(ctx, `SELECT StarDust FROM starduststore_players WHERE SteamID = ? ORDER BY id DESC LIMIT 1 FOR UPDATE`, steamID).Scan(&balance)
	if errors.Is(err, sql.ErrNoRows) {
		balance = 0
		playerExists = false
	} else if err != nil {
		return 0, fmt.Errorf("lock stardust balance: %w", err)
	}
	if balance < price {
		return 0, ErrInsufficientStardust
	}
	if playerExists {
		if _, err := tx.ExecContext(ctx, `UPDATE starduststore_players SET StarDust = StarDust - ? WHERE SteamID = ?`, price, steamID); err != nil {
			return 0, fmt.Errorf("deduct stardust: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO starduststore_items (SteamID, Price, Type, UniqueId, DateOfPurchase, DateOfExpiration)
		VALUES (?, ?, ?, ?, NOW(), '0001-01-01 00:00:00')
	`, steamID, price, itemType, uniqueID); err != nil {
		return 0, fmt.Errorf("grant stardust item: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit stardust purchase: %w", err)
	}
	return balance - price, nil
}

func (r *Repository) Announcements(ctx context.Context) ([]domain.Announcement, error) {
	items, err := r.announcementCache.getOrLoad(publicCatalogTTL, func() ([]domain.Announcement, error) {
		return r.loadAnnouncements(ctx)
	})
	if err != nil {
		return nil, err
	}
	return append([]domain.Announcement(nil), items...), nil
}

func (r *Repository) loadAnnouncements(ctx context.Context) ([]domain.Announcement, error) {
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

	type announcementRow struct {
		id          uint64
		title       string
		payload     string
		kind        int
		publishedAt time.Time
		coverPath   string
		detailPath  string
	}
	scanned := make([]announcementRow, 0)
	imageIDs := make([]uint64, 0)
	for rows.Next() {
		var (
			id               uint64
			title            string
			payload          sql.NullString
			announcementType int
			published        sql.NullTime
			created          time.Time
			coverPath        string
			detailPath       string
		)
		if err := rows.Scan(&id, &title, &payload, &announcementType, &published, &created, &coverPath, &detailPath); err != nil {
			return nil, fmt.Errorf("scan announcement: %w", err)
		}
		publishedAt := created
		if published.Valid {
			publishedAt = published.Time
		}
		payloadText := payload.String
		scanned = append(scanned, announcementRow{
			id:          id,
			title:       title,
			payload:     payloadText,
			kind:        announcementType,
			publishedAt: publishedAt,
			coverPath:   coverPath,
			detailPath:  detailPath,
		})
		imageIDs = append(imageIDs, collectAnnouncementImageIDs(payloadText)...)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate announcements: %w", err)
	}
	// Close the result set before resolving payload images so we never hold a
	// rows cursor while borrowing more connections from the tiny pool.
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close announcement rows: %w", err)
	}

	paths, err := r.loadFileRelativePaths(ctx, imageIDs)
	if err != nil {
		return nil, fmt.Errorf("load announcement images: %w", err)
	}

	items := make([]domain.Announcement, 0, len(scanned))
	for _, row := range scanned {
		renderPayload, err := resolveAnnouncementPayloadWithFiles(row.payload, paths)
		if err != nil {
			return nil, fmt.Errorf("resolve announcement %d payload: %w", row.id, err)
		}
		items = append(items, domain.Announcement{
			ID:             strconv.FormatUint(row.id, 10),
			Title:          row.title,
			Content:        announcementSummary(row.payload),
			Level:          map[bool]string{true: "event", false: "info"}[row.kind == 1],
			Dismissible:    true,
			DisplayDate:    row.publishedAt.Format("01 / 02"),
			PublishedAt:    row.publishedAt.Format(time.RFC3339),
			CoverImageURL:  publicFileURL(row.coverPath),
			DetailImageURL: publicFileURL(row.detailPath),
			RenderPayload:  renderPayload,
		})
	}
	return items, nil
}

func (r *Repository) StoreItems(ctx context.Context) ([]domain.StoreItem, error) {
	items, err := r.storeItemCache.getOrLoad(publicCatalogTTL, func() ([]domain.StoreItem, error) {
		return r.loadStoreItems(ctx)
	})
	if err != nil {
		return nil, err
	}
	return append([]domain.StoreItem(nil), items...), nil
}

func (r *Repository) loadStoreItems(ctx context.Context) ([]domain.StoreItem, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT pp.id, p.id, p.name, p.desc, p.label, p.type, p.rarity_id,
		       COALESCE(r.name, ''), pp.price, pp.sort, COALESCE(f.relative_path, ''),
		       COALESCE(pp.days, 0), COALESCE(pp.quantity, 1),
		       COALESCE(NULLIF(TRIM(p.mode), ''), 'ALL')
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
		WHERE pp.state = 1 AND pp.currency_id = 1`+starlightStoreProductFilter+`
		ORDER BY pp.sort ASC, p.rarity_id DESC, p.id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("query store items: %w", err)
	}
	defer rows.Close()
	items := make([]domain.StoreItem, 0)
	for rows.Next() {
		var pricingID, productID int64
		var name, description, label, rarityName, relativePath, mode string
		var productType, rarityID, price, sortOrder, days, quantity int
		if err := rows.Scan(&pricingID, &productID, &name, &description, &label, &productType, &rarityID, &rarityName, &price, &sortOrder, &relativePath, &days, &quantity, &mode); err != nil {
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
			Days:            days,
			Quantity:        quantity,
			Icon:            icon,
			Tone:            rarityTone(rarityID),
			Tag:             displayRarity(rarityID, rarityName),
			Enabled:         true,
			Sort:            sortOrder,
			ImageURL:        publicFileURL(relativePath),
			Mode:            mode,
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

// StoreItemsForPlayer 返回面向指定玩家的商城列表：已持有永久版本（expired 为 NULL 或 >= '9000-01-01' 哨兵）的
// 非数量型星光商品会被隐藏，与 StarLightStore 插件的商店可见性及 PurchaseStarlight 的拦截保持同一口径；
// 星尘物品每件只能持有一份，已持有的也一并隐藏。
func (r *Repository) StoreItemsForPlayer(ctx context.Context, steamID uint64) ([]domain.StoreItem, error) {
	items, err := r.StoreItems(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT i.product_id
		FROM sls_player_inventory AS i
		INNER JOIN sls_product AS p ON p.id = i.product_id
		WHERE i.steamid = ?
		  AND p.type != 2
		  AND (i.expired IS NULL OR i.expired >= '9000-01-01 00:00:00')
	`, steamID)
	if err != nil {
		return nil, fmt.Errorf("query permanently owned products: %w", err)
	}
	defer rows.Close()
	owned := make(map[int64]struct{})
	for rows.Next() {
		var productID int64
		if err := rows.Scan(&productID); err != nil {
			return nil, fmt.Errorf("scan permanently owned product: %w", err)
		}
		owned[productID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate permanently owned products: %w", err)
	}

	ownedStardust := make(map[string]struct{})
	if r.challengeDB != nil && r.challengeCatalogAvailable {
		stardustRows, err := r.challengeDB.QueryContext(ctx, `
			SELECT Type, UniqueId
			FROM starduststore_items
			WHERE SteamID = ?
			  AND (DateOfExpiration IS NULL
			       OR DateOfExpiration < '1000-01-01 00:00:00'
			       OR DateOfExpiration > NOW())
		`, steamID)
		if err != nil {
			return nil, fmt.Errorf("query owned stardust items: %w", err)
		}
		defer stardustRows.Close()
		for stardustRows.Next() {
			var itemType, uniqueID string
			if err := stardustRows.Scan(&itemType, &uniqueID); err != nil {
				return nil, fmt.Errorf("scan owned stardust item: %w", err)
			}
			ownedStardust[itemType+"\x1f"+uniqueID] = struct{}{}
		}
		if err := stardustRows.Err(); err != nil {
			return nil, fmt.Errorf("iterate owned stardust items: %w", err)
		}
	}

	if len(owned) == 0 && len(ownedStardust) == 0 {
		return items, nil
	}
	filtered := items[:0]
	for _, item := range items {
		switch item.PurchaseBackend {
		case "star-product":
			if productID, parseErr := strconv.ParseInt(item.ExternalID, 10, 64); parseErr == nil {
				if _, hidden := owned[productID]; hidden {
					continue
				}
			}
		case "challenge-stardust":
			if _, hidden := ownedStardust[item.StardustType+"\x1f"+item.ExternalID]; hidden {
				continue
			}
		}
		filtered = append(filtered, item)
	}
	return filtered, nil
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
			Price:           price,
			Icon:            icon,
			Tone:            "from-secondary to-violet-600",
			Tag:             category,
			Enabled:         true,
			Sort:            sortOrder,
			StardustType:    itemType,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate challenge store items: %w", err)
	}
	return items, nil
}

func (r *Repository) Maps(ctx context.Context) ([]domain.MapResource, error) {
	items, err := r.mapCache.getOrLoad(publicCatalogTTL, func() ([]domain.MapResource, error) {
		return r.loadMaps(ctx)
	})
	if err != nil {
		return nil, err
	}
	return append([]domain.MapResource(nil), items...), nil
}

func (r *Repository) loadMaps(ctx context.Context) ([]domain.MapResource, error) {
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

func (r *Repository) WorkshopPacks(ctx context.Context, mode string) ([]domain.WorkshopPack, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, kind, mode, title, description, workshop_id
		FROM launcher_workshop_pack
		WHERE enabled = 1
		  AND ((kind = 'base' AND mode = 'ALL') OR (kind = 'mode' AND mode = ?))
		ORDER BY CASE kind WHEN 'base' THEN 0 ELSE 1 END, sort_order ASC, id ASC
	`, strings.ToUpper(strings.TrimSpace(mode)))
	if err != nil {
		return nil, fmt.Errorf("query workshop packs: %w", err)
	}
	defer rows.Close()

	packs := make([]domain.WorkshopPack, 0)
	for rows.Next() {
		var pack domain.WorkshopPack
		var workshopID uint64
		if err := rows.Scan(&pack.ID, &pack.Kind, &pack.Mode, &pack.Title, &pack.Description, &workshopID); err != nil {
			return nil, fmt.Errorf("scan workshop pack: %w", err)
		}
		pack.WorkshopID = strconv.FormatUint(workshopID, 10)
		pack.WorkshopURL = workshopPageURL(pack.WorkshopID)
		pack.SteamURL = "steam://openurl/" + pack.WorkshopURL
		packs = append(packs, pack)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workshop packs: %w", err)
	}
	return packs, nil
}

func workshopPageURL(workshopID string) string {
	return "https://steamcommunity.com/workshop/filedetails/?id=" + url.QueryEscape(strings.TrimSpace(workshopID))
}

// LatestLauncherRelease 返回最新一条启动器发布记录；无记录时返回零值（Version == ""）。
func (r *Repository) LatestLauncherRelease(ctx context.Context) (domain.LauncherRelease, error) {
	var release domain.LauncherRelease
	var mandatory int
	err := r.db.QueryRowContext(ctx, `
		SELECT id, version, mandatory, changelog, artifact_url, signature, pub_date
		FROM launcher_release
		ORDER BY pub_date DESC, id DESC
		LIMIT 1
	`).Scan(&release.ID, &release.Version, &mandatory, &release.Changelog,
		&release.ArtifactURL, &release.Signature, &release.PubDate)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LauncherRelease{}, nil
	}
	if err != nil {
		return domain.LauncherRelease{}, fmt.Errorf("query latest launcher release: %w", err)
	}
	release.Mandatory = mandatory != 0
	return release, nil
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
	storeItems, err := r.StoreItemsForPlayer(ctx, steamID)
	if err != nil {
		return domain.PlayerReadModel{}, err
	}
	return domain.PlayerReadModel{Account: account, Inventory: inventory, PurchaseHistory: history, SeasonPass: seasonPass, Penalties: penalties, StoreItems: storeItems}, nil
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
	item.DailyQuestStatus = map[string]int{}
	item.WeeklyQuestStatus = map[string]int{}
	var dailyQuestStatus string
	var dailyLoggedIn int
	if err := r.db.QueryRowContext(ctx, `SELECT games_completed, online_minutes, has_logged_in, quest_status FROM season_pass_daily_quests WHERE steam_id64 = ? ORDER BY quest_date DESC LIMIT 1`, steamID).
		Scan(&item.DailyGames, &item.DailyOnlineMinutes, &dailyLoggedIn, &dailyQuestStatus); err == nil {
		item.DailyLoggedIn = dailyLoggedIn != 0
		item.DailyQuestStatus = parseQuestStatus(dailyQuestStatus)
	}
	var completedModes string
	var weeklyQuestStatus string
	var weeklyLoggedIn int
	if err := r.db.QueryRowContext(ctx, `SELECT games_completed, completed_modes, has_logged_in, quest_status FROM season_pass_weekly_quests WHERE steam_id64 = ? ORDER BY week_start_date DESC LIMIT 1`, steamID).
		Scan(&item.WeeklyGames, &completedModes, &weeklyLoggedIn, &weeklyQuestStatus); err == nil {
		item.WeeklyLoggedIn = weeklyLoggedIn != 0
		item.WeeklyQuestStatus = parseQuestStatus(weeklyQuestStatus)
	}
	item.WeeklyCompletedModes = collectionCount(completedModes)
	return item, nil
}

// parseQuestStatus 解析任务完成状态 JSON（形如 {"1":2,"5":2}，key 为任务 ID，值 >= 2 表示已完成）。
func parseQuestStatus(raw string) map[string]int {
	status := make(map[string]int)
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return status
	}
	if err := json.Unmarshal([]byte(trimmed), &status); err != nil {
		return make(map[string]int)
	}
	return status
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

func collectAnnouncementImageIDs(raw string) []uint64 {
	payload := announcementPayload(raw)
	var document map[string]any
	if err := json.Unmarshal(payload, &document); err != nil {
		return nil
	}
	ids := make([]uint64, 0)
	seen := make(map[uint64]struct{})
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
			id := uint64(imageID)
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	return ids
}

func (r *Repository) loadFileRelativePaths(ctx context.Context, ids []uint64) (map[uint64]string, error) {
	paths := make(map[uint64]string, len(ids))
	if len(ids) == 0 {
		return paths, nil
	}
	unique := make([]uint64, 0, len(ids))
	seen := make(map[uint64]struct{}, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return paths, nil
	}

	placeholders := make([]string, len(unique))
	args := make([]any, len(unique))
	for index, id := range unique {
		placeholders[index] = "?"
		args[index] = id
	}
	query := `SELECT id, COALESCE(relative_path, '') FROM scs_file WHERE id IN (` + strings.Join(placeholders, ",") + `)`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uint64
		var relativePath string
		if err := rows.Scan(&id, &relativePath); err != nil {
			return nil, err
		}
		paths[id] = relativePath
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return paths, nil
}

func resolveAnnouncementPayloadWithFiles(raw string, paths map[uint64]string) (json.RawMessage, error) {
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
			relativePath := paths[uint64(imageID)]
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
	case strings.Contains(normalized, "playerskin"), strings.Contains(normalized, "playermodel"), strings.Contains(normalized, "player_skin"), strings.Contains(normalized, "agent"):
		return "角色外观", "user-round"
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

// formatExpiry converts a nullable expiry column to RFC3339.
// NULL and the StarLightStore permanent sentinel (>= 9000-01-01) both mean permanent (empty string).
func formatExpiry(expired sql.NullTime) string {
	if !expired.Valid || expired.Time.Year() >= 9000 {
		return ""
	}
	return expired.Time.Format(time.RFC3339)
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

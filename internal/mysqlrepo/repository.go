package mysqlrepo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

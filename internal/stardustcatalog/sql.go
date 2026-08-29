package stardustcatalog

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
)

const catalogDDL = `CREATE TABLE IF NOT EXISTS starduststore_catalog (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    item_type VARCHAR(32) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
    unique_id VARCHAR(256) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
    group_name VARCHAR(128) NOT NULL,
    category_name VARCHAR(128) NOT NULL,
    display_name VARCHAR(255) NOT NULL,
    price INT UNSIGNED NOT NULL DEFAULT 0,
    slot INT NOT NULL DEFAULT 0,
    hidden TINYINT(1) NOT NULL DEFAULT 0,
    purchasable TINYINT(1) NOT NULL DEFAULT 0,
    enabled TINYINT(1) NOT NULL DEFAULT 1,
    restricted_steam_id BIGINT UNSIGNED NULL,
    config_json JSON NOT NULL,
    sort_order INT UNSIGNED NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uk_starduststore_catalog_item (item_type, unique_id),
    KEY idx_starduststore_catalog_shop (enabled, purchasable, hidden, category_name, sort_order)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`

// WriteImportSQL emits a self-contained MySQL catalog migration and snapshot.
// Text is represented as UTF-8 hex expressions so JSON, quotes and Chinese
// labels are independent of the SQL client's escaping and source encoding.
func WriteImportSQL(writer io.Writer, items []Item) error {
	buffer := bufio.NewWriter(writer)
	if _, err := fmt.Fprintln(buffer, "-- Generated from StarDustStore.json. Re-running this file is idempotent."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(buffer, "SET NAMES utf8mb4;\nSET @STARCS_OLD_SQL_SAFE_UPDATES = @@SQL_SAFE_UPDATES;\nSET SQL_SAFE_UPDATES = 0;"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(buffer, catalogDDL); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(buffer, "\nSTART TRANSACTION;\nUPDATE starduststore_catalog SET enabled = 0;"); err != nil {
		return err
	}

	const batchSize = 100
	for start := 0; start < len(items); start += batchSize {
		end := min(start+batchSize, len(items))
		if _, err := fmt.Fprintln(buffer, `INSERT INTO starduststore_catalog
    (item_type, unique_id, group_name, category_name, display_name, price, slot,
     hidden, purchasable, enabled, restricted_steam_id, config_json, sort_order)
VALUES`); err != nil {
			return err
		}
		for index, item := range items[start:end] {
			restricted := "NULL"
			if item.RestrictedSteamID != "" {
				if _, err := strconv.ParseUint(item.RestrictedSteamID, 10, 64); err != nil {
					return fmt.Errorf("invalid restricted Steam64 for %s/%s: %w", item.Type, item.UniqueID, err)
				}
				restricted = item.RestrictedSteamID
			}
			terminator := ","
			if index == end-start-1 {
				terminator = ""
			}
			if _, err := fmt.Fprintf(buffer,
				"    (%s, %s, %s, %s, %s, %d, %d, %d, %d, 1, %s, CAST(%s AS JSON), %d)%s\n",
				hexUTF8(item.Type), hexUTF8(item.UniqueID), hexUTF8(item.Group), hexUTF8(item.Category),
				hexUTF8(item.DisplayName), item.Price, item.Slot, boolInt(item.Hidden), boolInt(item.Purchasable),
				restricted, hexUTF8(string(item.ConfigJSON)), item.Sort, terminator,
			); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(buffer, `ON DUPLICATE KEY UPDATE
    group_name = VALUES(group_name), category_name = VALUES(category_name),
    display_name = VALUES(display_name), price = VALUES(price), slot = VALUES(slot),
    hidden = VALUES(hidden), purchasable = VALUES(purchasable), enabled = 1,
    restricted_steam_id = VALUES(restricted_steam_id), config_json = VALUES(config_json),
    sort_order = VALUES(sort_order);`); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(buffer, "COMMIT;\nSET SQL_SAFE_UPDATES = @STARCS_OLD_SQL_SAFE_UPDATES;"); err != nil {
		return err
	}
	return buffer.Flush()
}

func hexUTF8(value string) string {
	return "CONVERT(0x" + hex.EncodeToString([]byte(value)) + " USING utf8mb4)"
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

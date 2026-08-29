package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/starcs/star-launcher-backend/internal/config"
	"github.com/starcs/star-launcher-backend/internal/stardustcatalog"
)

func main() {
	filePath := flag.String("file", "StarDustStore.json", "path to StarDustStore.json")
	dryRun := flag.Bool("dry-run", false, "validate and summarize without connecting to MySQL")
	flag.Parse()

	items, err := stardustcatalog.Load(*filePath)
	if err != nil {
		log.Fatal(err)
	}
	printSummary(items)
	if *dryRun {
		return
	}
	if _, err := config.LoadDotEnv(); err != nil {
		log.Fatal(err)
	}
	dsn, configured, err := config.ChallengeImportDSN()
	if err != nil {
		log.Fatal(err)
	}
	if !configured {
		log.Fatal("STAR_DB_CHALLENGE_IMPORT_DSN is required and must use a database account with catalog write permissions")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		log.Fatal(err)
	}
	if err := importCatalog(ctx, db, items); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("imported %d catalog items\n", len(items))
}

func importCatalog(ctx context.Context, db *sql.DB, items []stardustcatalog.Item) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "UPDATE starduststore_catalog SET enabled = 0"); err != nil {
		return fmt.Errorf("disable previous catalog: %w (apply migrations/001_starduststore_catalog.sql first)", err)
	}
	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO starduststore_catalog
			(item_type, unique_id, group_name, category_name, display_name, price, slot,
			 hidden, purchasable, enabled, restricted_steam_id, config_json, sort_order)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			group_name = VALUES(group_name), category_name = VALUES(category_name),
			display_name = VALUES(display_name), price = VALUES(price), slot = VALUES(slot),
			hidden = VALUES(hidden), purchasable = VALUES(purchasable), enabled = 1,
			restricted_steam_id = VALUES(restricted_steam_id), config_json = VALUES(config_json),
			sort_order = VALUES(sort_order)
	`)
	if err != nil {
		return err
	}
	defer statement.Close()
	for _, item := range items {
		var restrictedSteamID any
		if item.RestrictedSteamID != "" {
			value, err := strconv.ParseUint(item.RestrictedSteamID, 10, 64)
			if err != nil {
				return fmt.Errorf("invalid restricted Steam64 for %s/%s: %w", item.Type, item.UniqueID, err)
			}
			restrictedSteamID = value
		}
		if _, err := statement.ExecContext(ctx, item.Type, item.UniqueID, item.Group, item.Category,
			item.DisplayName, item.Price, item.Slot, item.Hidden, item.Purchasable,
			restrictedSteamID, item.ConfigJSON, item.Sort); err != nil {
			return fmt.Errorf("upsert %s/%s: %w", item.Type, item.UniqueID, err)
		}
	}
	return tx.Commit()
}

func printSummary(items []stardustcatalog.Item) {
	categories := map[string]int{}
	purchasable := 0
	for _, item := range items {
		categories[item.Category]++
		if item.Purchasable {
			purchasable++
		}
	}
	keys := make([]string, 0, len(categories))
	for key := range categories {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	fmt.Fprintf(os.Stdout, "catalog items=%d purchasable=%d categories=%d\n", len(items), purchasable, len(categories))
	for _, key := range keys {
		fmt.Fprintf(os.Stdout, "%s=%d\n", key, categories[key])
	}
}

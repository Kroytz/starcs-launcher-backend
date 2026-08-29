package stardustcatalog

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

type Item struct {
	Type              string
	UniqueID          string
	Group             string
	Category          string
	DisplayName       string
	Price             uint64
	Slot              int
	Hidden            bool
	Purchasable       bool
	RestrictedSteamID string
	Sort              int
	ConfigJSON        []byte
}

func Load(path string) ([]Item, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return Parse(file)
}

func Parse(reader io.Reader) ([]Item, error) {
	var root struct {
		Items map[string]any `json:"Items"`
	}
	decoder := json.NewDecoder(reader)
	decoder.UseNumber()
	if err := decoder.Decode(&root); err != nil {
		return nil, fmt.Errorf("decode StarDustStore JSON: %w", err)
	}
	if len(root.Items) == 0 {
		return nil, fmt.Errorf("StarDustStore JSON does not contain Items")
	}

	items := make([]Item, 0)
	if err := walk(root.Items, nil, "", &items); err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(items))
	for index := range items {
		items[index].Sort = index + 1
		key := items[index].Type + "\x00" + items[index].UniqueID
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("duplicate catalog item type=%q uniqueid=%q", items[index].Type, items[index].UniqueID)
		}
		seen[key] = struct{}{}
	}
	return items, nil
}

func walk(node map[string]any, path []string, restrictedSteamID string, items *[]Item) error {
	if flag := valueString(node["flag"]); flag != "" {
		restrictedSteamID = flag
	}
	keys := make([]string, 0, len(node))
	for key, value := range node {
		if _, ok := value.(map[string]any); ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		child := node[key].(map[string]any)
		if valueString(child["uniqueid"]) != "" && valueString(child["type"]) != "" {
			item, err := catalogItem(key, child, path, restrictedSteamID)
			if err != nil {
				return err
			}
			*items = append(*items, item)
			continue
		}
		if err := walk(child, append(path, key), restrictedSteamID, items); err != nil {
			return err
		}
	}
	return nil
}

func catalogItem(displayName string, raw map[string]any, path []string, restrictedSteamID string) (Item, error) {
	itemType := strings.TrimSpace(valueString(raw["type"]))
	uniqueID := strings.TrimSpace(valueString(raw["uniqueid"]))
	price, err := parseUintField(raw["price"])
	if err != nil {
		return Item{}, fmt.Errorf("item %s/%s has invalid price: %w", itemType, uniqueID, err)
	}
	slotValue, err := parseUintField(raw["slot"])
	if err != nil {
		return Item{}, fmt.Errorf("item %s/%s has invalid slot: %w", itemType, uniqueID, err)
	}
	hidden := parseBoolField(raw["hidden"])
	group, category := "未分类", "未分类"
	if len(path) > 0 {
		group = path[0]
		category = path[len(path)-1]
	}
	configJSON, err := json.Marshal(raw)
	if err != nil {
		return Item{}, fmt.Errorf("encode item %s/%s: %w", itemType, uniqueID, err)
	}
	return Item{
		Type:              itemType,
		UniqueID:          uniqueID,
		Group:             group,
		Category:          category,
		DisplayName:       displayName,
		Price:             price,
		Slot:              int(slotValue),
		Hidden:            hidden,
		Purchasable:       price > 0 && price < 99999 && !hidden && restrictedSteamID == "",
		RestrictedSteamID: restrictedSteamID,
		ConfigJSON:        configJSON,
	}, nil
}

func valueString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return ""
	}
}

func parseUintField(value any) (uint64, error) {
	raw := strings.TrimSpace(valueString(value))
	if raw == "" {
		return 0, nil
	}
	return strconv.ParseUint(raw, 10, 64)
}

func parseBoolField(value any) bool {
	raw := strings.TrimSpace(valueString(value))
	parsed, _ := strconv.ParseBool(raw)
	return parsed
}

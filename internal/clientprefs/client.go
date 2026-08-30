package clientprefs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/starcs/star-launcher-backend/internal/domain"
)

const stringPreferenceType = 0

type Client struct {
	baseURL      *url.URL
	apiKey       string
	apiKeyHeader string
	httpClient   *http.Client
}

type apiEnvelope[T any] struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data T      `json:"data"`
}

type preference struct {
	Key   string `json:"key"`
	Type  int    `json:"type"`
	Value string `json:"value"`
}

type updateRequest struct {
	Mode    string       `json:"mode"`
	SteamID string       `json:"steamid"`
	Plugin  string       `json:"plugin"`
	Prefs   []preference `json:"prefs"`
}

type modeSnapshot struct {
	preferences []preference
	equipment   domain.ModeEquipment
}

func New(baseURL, apiKey, apiKeyHeader string) (*Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("invalid client preferences API URL")
	}
	apiKey = strings.TrimSpace(apiKey)
	apiKeyHeader = strings.TrimSpace(apiKeyHeader)
	if apiKey == "" || apiKeyHeader == "" {
		return nil, errors.New("client preferences API key is required")
	}
	return &Client{
		baseURL:      parsed,
		apiKey:       apiKey,
		apiKeyHeader: apiKeyHeader,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func (c *Client) Load(ctx context.Context, steamID uint64) (domain.EquipmentProfile, error) {
	snapshots, err := c.loadAllModes(ctx, steamID)
	if err != nil {
		return domain.EquipmentProfile{}, err
	}
	return profileFromSnapshots(snapshots), nil
}

func (c *Client) Apply(ctx context.Context, steamID uint64, mutation domain.EquipmentMutation) (domain.EquipmentProfile, error) {
	snapshots, err := c.loadAllModes(ctx, steamID)
	if err != nil {
		return domain.EquipmentProfile{}, err
	}
	originals := make(map[string][]preference, len(mutation.Modes))
	for _, mode := range mutation.Modes {
		snapshot, ok := snapshots[mode]
		if !ok {
			return domain.EquipmentProfile{}, fmt.Errorf("unsupported equipment mode %q", mode)
		}
		originals[mode] = append([]preference(nil), snapshot.preferences...)
		applyMutation(&snapshot.equipment, mutation)
		updated, err := encodeEquipmentPreferences(snapshot.preferences, snapshot.equipment)
		if err != nil {
			return domain.EquipmentProfile{}, fmt.Errorf("encode %s equipment: %w", mode, err)
		}
		snapshot.preferences = updated
		snapshots[mode] = snapshot
	}

	savedModes := make([]string, 0, len(mutation.Modes))
	for _, mode := range mutation.Modes {
		if err := c.saveMode(ctx, steamID, mode, snapshots[mode].preferences); err != nil {
			rollbackErr := c.rollback(ctx, steamID, savedModes, originals)
			return domain.EquipmentProfile{}, errors.Join(fmt.Errorf("save %s equipment: %w", mode, err), rollbackErr)
		}
		savedModes = append(savedModes, mode)
	}
	return profileFromSnapshots(snapshots), nil
}

func (c *Client) loadAllModes(ctx context.Context, steamID uint64) (map[string]modeSnapshot, error) {
	loadCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		mode     string
		snapshot modeSnapshot
		err      error
	}
	results := make(chan result, len(domain.EquipmentModes))
	for _, mode := range domain.EquipmentModes {
		go func(mode string) {
			snapshot, err := c.loadMode(loadCtx, steamID, mode)
			results <- result{mode: mode, snapshot: snapshot, err: err}
		}(mode)
	}

	snapshots := make(map[string]modeSnapshot, len(domain.EquipmentModes))
	var firstErr error
	for range domain.EquipmentModes {
		result := <-results
		if result.err != nil && firstErr == nil {
			firstErr = fmt.Errorf("load %s equipment: %w", result.mode, result.err)
			cancel()
			continue
		}
		if result.err == nil {
			snapshots[result.mode] = result.snapshot
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return snapshots, nil
}

func (c *Client) loadMode(ctx context.Context, steamID uint64, mode string) (modeSnapshot, error) {
	path := fmt.Sprintf("cs2-client-pref/prefs/%s/%d", url.PathEscape(mode), steamID)
	request, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return modeSnapshot{}, err
	}
	var response apiEnvelope[map[string][]preference]
	if err := c.do(request, &response); err != nil {
		return modeSnapshot{}, err
	}
	preferences := append([]preference(nil), response.Data[domain.EquipmentPluginIdentity]...)
	equipment, err := decodeEquipmentPreferences(preferences)
	if err != nil {
		return modeSnapshot{}, err
	}
	return modeSnapshot{preferences: preferences, equipment: equipment}, nil
}

func (c *Client) saveMode(ctx context.Context, steamID uint64, mode string, preferences []preference) error {
	payload := updateRequest{
		Mode:    mode,
		SteamID: strconv.FormatUint(steamID, 10),
		Plugin:  domain.EquipmentPluginIdentity,
		Prefs:   preferences,
	}
	request, err := c.newRequest(ctx, http.MethodPost, "cs2-client-pref/prefs", payload)
	if err != nil {
		return err
	}
	var response apiEnvelope[[]preference]
	return c.do(request, &response)
}

func (c *Client) rollback(ctx context.Context, steamID uint64, modes []string, originals map[string][]preference) error {
	var rollbackErrors []error
	for index := len(modes) - 1; index >= 0; index-- {
		mode := modes[index]
		if err := c.saveMode(ctx, steamID, mode, originals[mode]); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("rollback %s equipment: %w", mode, err))
		}
	}
	return errors.Join(rollbackErrors...)
}

func (c *Client) newRequest(ctx context.Context, method, path string, payload any) (*http.Request, error) {
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/" + strings.TrimLeft(path, "/")
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return nil, err
	}
	request.Header.Set(c.apiKeyHeader, c.apiKey)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	return request, nil
}

func (c *Client) do(request *http.Request, target any) error {
	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("client preferences API returned HTTP %d", response.StatusCode)
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decode client preferences response: %w", err)
	}
	switch response := target.(type) {
	case *apiEnvelope[map[string][]preference]:
		if response.Code != 2000 {
			return fmt.Errorf("client preferences API error %d: %s", response.Code, response.Msg)
		}
	case *apiEnvelope[[]preference]:
		if response.Code != 2000 {
			return fmt.Errorf("client preferences API error %d: %s", response.Code, response.Msg)
		}
	}
	return nil
}

func decodeEquipmentPreferences(preferences []preference) (domain.ModeEquipment, error) {
	equipment := domain.NewModeEquipment()
	for _, preference := range preferences {
		switch preference.Key {
		case domain.PlayerSkinPreferenceKey:
			if err := json.Unmarshal([]byte(preference.Value), &equipment.PlayerSkin); err != nil {
				return domain.ModeEquipment{}, fmt.Errorf("decode p_s: %w", err)
			}
		case domain.WeaponSkinPreferenceKey:
			if err := json.Unmarshal([]byte(preference.Value), &equipment.WeaponSkin); err != nil {
				return domain.ModeEquipment{}, fmt.Errorf("decode w_s: %w", err)
			}
		}
	}
	if equipment.WeaponSkin.PlayerSkinExclusive == nil {
		equipment.WeaponSkin.PlayerSkinExclusive = make(map[string]map[string]int64)
	}
	if equipment.WeaponSkin.Weapons == nil {
		equipment.WeaponSkin.Weapons = make(map[string]map[string]int64)
	}
	return equipment, nil
}

func encodeEquipmentPreferences(preferences []preference, equipment domain.ModeEquipment) ([]preference, error) {
	playerSkin, err := json.Marshal(equipment.PlayerSkin)
	if err != nil {
		return nil, err
	}
	weaponSkin, err := json.Marshal(equipment.WeaponSkin)
	if err != nil {
		return nil, err
	}
	updated := append([]preference(nil), preferences...)
	updated = setPreference(updated, domain.PlayerSkinPreferenceKey, string(playerSkin))
	updated = setPreference(updated, domain.WeaponSkinPreferenceKey, string(weaponSkin))
	return updated, nil
}

func setPreference(preferences []preference, key, value string) []preference {
	for index := range preferences {
		if preferences[index].Key == key {
			preferences[index].Type = stringPreferenceType
			preferences[index].Value = value
			return preferences
		}
	}
	return append(preferences, preference{Key: key, Type: stringPreferenceType, Value: value})
}

func applyMutation(equipment *domain.ModeEquipment, mutation domain.EquipmentMutation) {
	if mutation.Slot == "player" {
		applyPlayerSkinMutation(&equipment.PlayerSkin, mutation)
		return
	}
	applyWeaponSkinMutation(&equipment.WeaponSkin, mutation)
}

func applyPlayerSkinMutation(preference *domain.PlayerSkinPreference, mutation domain.EquipmentMutation) {
	teams := []string{mutation.Team}
	if mutation.Team == "all" {
		teams = []string{"ct", "t"}
	}
	for _, team := range teams {
		target := &preference.CT
		if team == "t" {
			target = &preference.T
		}
		if mutation.Equip {
			*target = mutation.ProductID
		} else if *target == mutation.ProductID {
			*target = 0
		}
	}
}

func applyWeaponSkinMutation(preference *domain.WeaponSkinPreference, mutation domain.EquipmentMutation) {
	if mutation.ExclusiveFor != "" {
		weapons := preference.PlayerSkinExclusive[mutation.ExclusiveFor]
		if weapons == nil && mutation.Equip {
			weapons = make(map[string]int64)
			preference.PlayerSkinExclusive[mutation.ExclusiveFor] = weapons
		}
		if mutation.Equip {
			weapons[mutation.WeaponType] = mutation.ProductID
		} else if weapons[mutation.WeaponType] == mutation.ProductID {
			delete(weapons, mutation.WeaponType)
			if len(weapons) == 0 {
				delete(preference.PlayerSkinExclusive, mutation.ExclusiveFor)
			}
		}
		return
	}
	prefabs := preference.Weapons[mutation.WeaponType]
	if prefabs == nil && mutation.Equip {
		prefabs = make(map[string]int64)
		preference.Weapons[mutation.WeaponType] = prefabs
	}
	if mutation.Equip {
		prefabs[mutation.WeaponPrefab] = mutation.ProductID
	} else if prefabs[mutation.WeaponPrefab] == mutation.ProductID {
		delete(prefabs, mutation.WeaponPrefab)
		if len(prefabs) == 0 {
			delete(preference.Weapons, mutation.WeaponType)
		}
	}
}

func profileFromSnapshots(snapshots map[string]modeSnapshot) domain.EquipmentProfile {
	profile := domain.NewEquipmentProfile()
	for _, mode := range domain.EquipmentModes {
		profile.Modes[mode] = snapshots[mode].equipment
	}
	return profile
}

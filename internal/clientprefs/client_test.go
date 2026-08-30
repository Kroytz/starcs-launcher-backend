package clientprefs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/starcs/star-launcher-backend/internal/domain"
)

type fakePreferenceServer struct {
	mu          sync.Mutex
	modes       map[string][]preference
	failedModes map[string]int
}

func newFakePreferenceServer() *fakePreferenceServer {
	server := &fakePreferenceServer{modes: make(map[string][]preference), failedModes: make(map[string]int)}
	for _, mode := range domain.EquipmentModes {
		server.modes[mode] = []preference{
			{Key: domain.PlayerSkinPreferenceKey, Type: stringPreferenceType, Value: `{"ct":0,"t":0}`},
			{Key: domain.WeaponSkinPreferenceKey, Type: stringPreferenceType, Value: `{"player_skin_exclusive":{},"weapons":{}}`},
			{Key: "p_s_b_g", Type: stringPreferenceType, Value: `{"keep":true}`},
		}
	}
	return server
}

func (server *fakePreferenceServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Star-Api-Key") != "test-key" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	server.mu.Lock()
	defer server.mu.Unlock()

	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/cs2-client-pref/prefs/") {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/cs2-client-pref/prefs/"), "/")
		mode := parts[0]
		if status := server.failedModes[mode]; status != 0 {
			http.Error(w, "mode unavailable", status)
			return
		}
		_ = json.NewEncoder(w).Encode(apiEnvelope[map[string][]preference]{
			Code: 2000,
			Msg:  "success",
			Data: map[string][]preference{domain.EquipmentPluginIdentity: server.modes[mode]},
		})
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == "/api/cs2-client-pref/prefs" {
		var request updateRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		server.modes[request.Mode] = append([]preference(nil), request.Prefs...)
		_ = json.NewEncoder(w).Encode(apiEnvelope[[]preference]{Code: 2000, Msg: "success", Data: request.Prefs})
		return
	}
	http.NotFound(w, r)
}

func TestLoadKeepsAvailableModesWhenOneModeFails(t *testing.T) {
	preferenceServer := newFakePreferenceServer()
	preferenceServer.failedModes["SCP"] = http.StatusForbidden
	server := httptest.NewServer(preferenceServer)
	defer server.Close()
	client, err := New(server.URL+"/api/", "test-key", "X-Star-Api-Key")
	if err != nil {
		t.Fatal(err)
	}

	profile, err := client.Load(context.Background(), 76561198000000001)
	if err != nil {
		t.Fatalf("partial mode failure should not fail the profile: %v", err)
	}
	if !strings.Contains(profile.UnavailableModes["SCP"], "HTTP 403") {
		t.Fatalf("expected SCP to be unavailable with its status, got %#v", profile.UnavailableModes)
	}
	if _, ok := profile.Modes["ZM"]; !ok {
		t.Fatal("available ZM profile should still be returned")
	}
	if _, ok := profile.Modes["SCP"]; !ok {
		t.Fatal("unavailable SCP should retain an empty shape for client compatibility")
	}
}

func TestApplyPlayerAndWeaponEquipment(t *testing.T) {
	preferenceServer := newFakePreferenceServer()
	server := httptest.NewServer(preferenceServer)
	defer server.Close()
	client, err := New(server.URL+"/api/", "test-key", "X-Star-Api-Key")
	if err != nil {
		t.Fatal(err)
	}

	profile, err := client.Apply(context.Background(), 76561198000000001, domain.EquipmentMutation{
		ProductID: 42,
		Slot:      "player",
		Modes:     []string{"ZM", "SCP"},
		Team:      "all",
		Equip:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if profile.Modes["ZM"].PlayerSkin.CT != 42 || profile.Modes["ZM"].PlayerSkin.T != 42 {
		t.Fatalf("unexpected ZM player skin: %+v", profile.Modes["ZM"].PlayerSkin)
	}
	if profile.Modes["SCP"].PlayerSkin.CT != 42 || profile.Modes["AFK"].PlayerSkin.CT != 0 {
		t.Fatalf("mutation should only affect selected modes: %+v", profile.Modes)
	}
	assertPreferencePreserved(t, preferenceServer.modes["ZM"], "p_s_b_g")

	profile, err = client.Apply(context.Background(), 76561198000000001, domain.EquipmentMutation{
		ProductID:    77,
		Slot:         "weapon",
		Modes:        []string{"ZM"},
		Team:         "all",
		WeaponType:   "CommonRifle",
		WeaponPrefab: "weapon_ak47",
		Equip:        true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := profile.Modes["ZM"].WeaponSkin.Weapons["CommonRifle"]["weapon_ak47"]; got != 77 {
		t.Fatalf("expected equipped weapon product 77, got %d", got)
	}

	profile, err = client.Apply(context.Background(), 76561198000000001, domain.EquipmentMutation{
		ProductID:    77,
		Slot:         "weapon",
		Modes:        []string{"ZM"},
		Team:         "all",
		WeaponType:   "CommonRifle",
		WeaponPrefab: "weapon_ak47",
		Equip:        false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := profile.Modes["ZM"].WeaponSkin.Weapons["CommonRifle"]; exists {
		t.Fatalf("weapon type should be removed after its final prefab is unequipped: %+v", profile.Modes["ZM"].WeaponSkin)
	}
}

func TestUnequipDoesNotClearReplacementItem(t *testing.T) {
	preferenceServer := newFakePreferenceServer()
	preferenceServer.modes["ZM"][0].Value = `{"ct":99,"t":99}`
	server := httptest.NewServer(preferenceServer)
	defer server.Close()
	client, err := New(server.URL+"/api/", "test-key", "X-Star-Api-Key")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := client.Apply(context.Background(), 76561198000000001, domain.EquipmentMutation{
		ProductID: 42,
		Slot:      "player",
		Modes:     []string{"ZM"},
		Team:      "all",
		Equip:     false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if profile.Modes["ZM"].PlayerSkin.CT != 99 || profile.Modes["ZM"].PlayerSkin.T != 99 {
		t.Fatalf("unequip must not clear a different equipped product: %+v", profile.Modes["ZM"].PlayerSkin)
	}
}

func assertPreferencePreserved(t *testing.T, preferences []preference, key string) {
	t.Helper()
	for _, preference := range preferences {
		if preference.Key == key {
			return
		}
	}
	t.Fatalf("preference %q was not preserved: %+v", key, preferences)
}

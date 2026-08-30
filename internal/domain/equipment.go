package domain

const (
	EquipmentPluginIdentity = "star_light_store"
	PlayerSkinPreferenceKey = "p_s"
	WeaponSkinPreferenceKey = "w_s"
)

var EquipmentModes = []string{"AFK", "JB", "MG", "SCP", "TTT", "ZE", "ZM"}

type EquipmentProfile struct {
	Version          int                      `json:"version"`
	Plugin           string                   `json:"plugin"`
	Modes            map[string]ModeEquipment `json:"modes"`
	UnavailableModes map[string]string        `json:"unavailableModes"`
}

type ModeEquipment struct {
	PlayerSkin PlayerSkinPreference `json:"p_s"`
	WeaponSkin WeaponSkinPreference `json:"w_s"`
}

type PlayerSkinPreference struct {
	CT int64 `json:"ct"`
	T  int64 `json:"t"`
}

type WeaponSkinPreference struct {
	PlayerSkinExclusive map[string]map[string]int64 `json:"player_skin_exclusive"`
	Weapons             map[string]map[string]int64 `json:"weapons"`
}

type EquipmentMutation struct {
	ProductID    int64
	Slot         string
	Modes        []string
	Team         string
	WeaponType   string
	WeaponPrefab string
	ExclusiveFor string
	Equip        bool
}

func NewEquipmentProfile() EquipmentProfile {
	return EquipmentProfile{
		Version:          2,
		Plugin:           EquipmentPluginIdentity,
		Modes:            make(map[string]ModeEquipment),
		UnavailableModes: make(map[string]string),
	}
}

func NewModeEquipment() ModeEquipment {
	return ModeEquipment{
		WeaponSkin: WeaponSkinPreference{
			PlayerSkinExclusive: make(map[string]map[string]int64),
			Weapons:             make(map[string]map[string]int64),
		},
	}
}

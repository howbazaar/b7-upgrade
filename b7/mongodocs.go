package b7

type SettingsDoc struct {
	DocID     string `bson:"_id"`
	ModelUUID string `bson:"model-uuid"`

	// Settings contains the settings. This must not be
	// omitempty, or migration cannot work correctly.
	Settings SettingsMap `bson:"settings"`

	// Version is a version number for the settings,
	// and is increased every time the settings change.
	Version int64 `bson:"version"`
}

type SettingsMap map[string]interface{}

type ModelDoc struct {
	UUID       string `bson:"_id"`
	Name       string
	Owner      string `bson:"owner"`
	ServerUUID string `bson:"server-uuid"`
}

type Address struct {
	Value       string `bson:"value"`
	AddressType string `bson:"addresstype"`
	Scope       string `bson:"networkscope,omitempty"`
	Origin      string `bson:"origin,omitempty"`
	SpaceName   string `bson:"spacename,omitempty"`
}

type MachineDoc struct {
	DocID                  string   `bson:"_id"`
	Id                     string   `bson:"machineid"`
	ModelUUID              string   `bson:"model-uuid"`
	PreferredPublicAddress Address  `bson:",omitempty"`
	SupportedContainers    []string `bson:",omitempty"`
}

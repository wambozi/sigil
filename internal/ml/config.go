package ml

// Config holds all ML engine configuration.
type Config struct {
	Mode         RoutingMode `toml:"mode"`
	RetrainEvery int         `toml:"retrain_every"` // retrain after N completed tasks (0 = manual only)
	Local        LocalConfig `toml:"local"`
	Cloud        CloudConfig `toml:"cloud"`
}

// LocalConfig configures the local sigil-ml sidecar.
type LocalConfig struct {
	Enabled   bool   `toml:"enabled"`
	ServerURL string `toml:"server_url"` // default: http://127.0.0.1:7774
	ServerBin string `toml:"server_bin"` // binary name or path (found via PATH)
}

// CloudConfig configures the cloud ML API.
type CloudConfig struct {
	Enabled bool   `toml:"enabled"`
	BaseURL string `toml:"base_url"`
	APIKey  string `toml:"api_key"`
}

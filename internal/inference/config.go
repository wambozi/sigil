package inference

// Config holds all inference backend configuration.
type Config struct {
	Mode  RoutingMode `toml:"mode"`
	Local LocalConfig `toml:"local"`
	Cloud CloudConfig `toml:"cloud"`
}

// LocalConfig configures the local inference backend (e.g. llama-server).
type LocalConfig struct {
	Enabled   bool   `toml:"enabled"`
	ServerURL string `toml:"server_url"` // default "http://127.0.0.1:8081"
	ServerBin string `toml:"server_bin"` // path to llama-server, empty = don't manage
	ModelPath string `toml:"model_path"` // path to .gguf file
	CtxSize   int    `toml:"ctx_size"`   // context window, default 4096
	GPULayers int    `toml:"gpu_layers"` // -1 = auto, 0 = CPU only
}

// CloudConfig configures a cloud inference provider.
type CloudConfig struct {
	Enabled  bool   `toml:"enabled"`
	Provider string `toml:"provider"` // "anthropic" or "openai"
	BaseURL  string `toml:"base_url"`
	APIKey   string `toml:"api_key"`
	Model    string `toml:"model"`
}

package update

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const autoCheckInterval = 6 * time.Hour

type updateConfig struct {
	AutoCheck   bool      `json:"auto_check"`
	LastCheckAt time.Time `json:"last_check_at"`
}

func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "zip233", "update.json"), nil
}

func loadConfig() updateConfig {
	var cfg updateConfig
	p, err := configPath()
	if err != nil {
		return cfg
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return cfg
	}
	_ = json.Unmarshal(b, &cfg)
	return cfg
}

func saveConfig(cfg updateConfig) {
	p, err := configPath()
	if err != nil {
		return
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, ".update-config-*.json")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	_ = os.Remove(p) // Windows cannot rename over an existing file.
	_ = os.Rename(tmpName, p)
}

// SetAutoCheck persists the automatic update-check preference.
func SetAutoCheck(enabled bool) {
	cfg := loadConfig()
	cfg.AutoCheck = enabled
	saveConfig(cfg)
}

// AutoCheckEnabled reports the persisted automatic update-check preference.
func AutoCheckEnabled() bool { return loadConfig().AutoCheck }

// timeSince and configNow are indirection points kept simple for clarity.
func timeSince(t time.Time) time.Duration { return time.Since(t) }
func configNow() time.Time                { return time.Now() }

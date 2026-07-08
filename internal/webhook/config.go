package webhook

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// LoadConfig reads the injector template config from a YAML file — the chart
// mounts this from a ConfigMap it renders from the same values as the V1 macro.
func LoadConfig(path string) (InjectorConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return InjectorConfig{}, fmt.Errorf("read injector config: %w", err)
	}
	var cfg InjectorConfig
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return InjectorConfig{}, fmt.Errorf("parse injector config: %w", err)
	}
	if cfg.ImagePullPolicy == "" {
		cfg.ImagePullPolicy = "IfNotPresent"
	}
	return cfg, nil
}

// Validate reports whether the config has the fields required to build a
// faithful injection.
func (c InjectorConfig) Validate() error {
	missing := ""
	switch {
	case c.AgentImage == "":
		missing = "agentImage"
	case c.APIURL == "":
		missing = "apiUrl"
	case c.SharedVolumeName == "":
		missing = "sharedVolumeName"
	case c.SharedMountPath == "":
		missing = "sharedMountPath"
	case c.PingKeysSecret == "":
		missing = "pingKeysSecret"
	}
	if missing != "" {
		return fmt.Errorf("injector config: %s is required", missing)
	}
	return nil
}

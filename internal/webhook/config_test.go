package webhook

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "injector.yaml")
	yaml := `
agentImage: ghcr.io/platops-security/crond-agent:0.3.0
imagePullPolicy: IfNotPresent
apiUrl: https://api.crond.io
sharedVolumeName: crond-agent-shared
sharedMountPath: /shared
pingKeysSecret: backups-pingkeys
captureOutput: true
redactPatterns:
  - 'Bearer [A-Za-z0-9._-]+'
containerSecurityContext:
  readOnlyRootFilesystem: true
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AgentImage != "ghcr.io/platops-security/crond-agent:0.3.0" ||
		cfg.APIURL != "https://api.crond.io" ||
		cfg.PingKeysSecret != "backups-pingkeys" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if len(cfg.RedactPatterns) != 1 || !cfg.CaptureOutput {
		t.Fatalf("nested fields wrong: %+v", cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestConfigValidate(t *testing.T) {
	base := InjectorConfig{
		AgentImage:       "img:1",
		APIURL:           "https://api.crond.io",
		SharedVolumeName: "v",
		SharedMountPath:  "/shared",
		PingKeysSecret:   "s",
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
	// each required field, cleared, must fail validation.
	for _, mut := range []func(c *InjectorConfig){
		func(c *InjectorConfig) { c.AgentImage = "" },
		func(c *InjectorConfig) { c.APIURL = "" },
		func(c *InjectorConfig) { c.SharedMountPath = "" },
		func(c *InjectorConfig) { c.PingKeysSecret = "" },
	} {
		c := base
		mut(&c)
		if err := c.Validate(); err == nil {
			t.Fatalf("expected validation error for %+v", c)
		}
	}
}

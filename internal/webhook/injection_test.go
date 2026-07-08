package webhook

import (
	"encoding/json"
	"testing"
)

func testConfig() InjectorConfig {
	return InjectorConfig{
		AgentImage:       "ghcr.io/platops-security/crond-agent:0.3.0",
		ImagePullPolicy:  "IfNotPresent",
		APIURL:           "https://api.crond.io",
		SharedVolumeName: "crond-agent-shared",
		SharedMountPath:  "/shared",
		PingKeysSecret:   "backups-pingkeys",
		CaptureOutput:    true,
		ContainerSecurityContext: map[string]any{
			"allowPrivilegeEscalation": false,
			"readOnlyRootFilesystem":   true,
		},
		InitResources: map[string]any{
			"limits": map[string]any{"cpu": "100m", "memory": "64Mi"},
		},
	}
}

// cronJobJSON builds a minimal CronJob object with the given annotations and a
// single container carrying command/args.
func cronJobJSON(t *testing.T, annotations map[string]string, container map[string]any) []byte {
	t.Helper()
	obj := map[string]any{
		"metadata": map[string]any{"annotations": annotations},
		"spec": map[string]any{
			"jobTemplate": map[string]any{
				"spec": map[string]any{
					"template": map[string]any{
						"spec": map[string]any{
							"containers": []any{container},
						},
					},
				},
			},
		},
	}
	b, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func findOp(ops []PatchOp, op, path string) *PatchOp {
	for i := range ops {
		if ops[i].Op == op && ops[i].Path == path {
			return &ops[i]
		}
	}
	return nil
}

const base = "/spec/jobTemplate/spec/template/spec"

func TestBuildInjection_Standard(t *testing.T) {
	raw := cronJobJSON(t,
		map[string]string{
			"crond.io/inject":       "true",
			"crond.io/ping-key-env": "PING_KEY_BACKUP",
		},
		map[string]any{
			"name":    "backup",
			"image":   "myco/backup:1.0",
			"command": []any{"/opt/backup.sh"},
			"args":    []any{"--full"},
		},
	)

	res, err := BuildInjection(raw, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	if res.Warning != "" {
		t.Fatalf("unexpected warning: %q", res.Warning)
	}

	// command rewritten to run through the shared-volume agent binary.
	cmdOp := findOp(res.Patch, "replace", base+"/containers/0/command")
	if cmdOp == nil {
		t.Fatalf("no command replace op; ops=%+v", res.Patch)
	}
	wantCmd := []any{
		"/shared/crond-agent", "exec",
		"--key=$(PING_KEY_BACKUP)", "--api-url=https://api.crond.io",
		"--", "/opt/backup.sh", "--full",
	}
	if !jsonEqual(cmdOp.Value, wantCmd) {
		t.Fatalf("wrapped command mismatch:\n got %s\nwant %s", toJSON(cmdOp.Value), toJSON(wantCmd))
	}

	// original args folded into the command, so the standalone args are removed.
	if findOp(res.Patch, "remove", base+"/containers/0/args") == nil {
		t.Fatalf("expected args removal; ops=%+v", res.Patch)
	}

	// shared emptyDir volume added (no pre-existing volumes → whole-array add).
	volOp := findOp(res.Patch, "add", base+"/volumes")
	if volOp == nil {
		t.Fatalf("no volumes add op; ops=%+v", res.Patch)
	}

	// installer init container added, using the configured agent image.
	initOp := findOp(res.Patch, "add", base+"/initContainers")
	if initOp == nil {
		t.Fatalf("no initContainers add op; ops=%+v", res.Patch)
	}
	if !containsJSON(initOp.Value, "ghcr.io/platops-security/crond-agent:0.3.0") ||
		!containsJSON(initOp.Value, "crond-agent-installer") ||
		!containsJSON(initOp.Value, "--target=/shared/crond-agent") {
		t.Fatalf("init container shape wrong: %s", toJSON(initOp.Value))
	}

	// ping-key env bound via secretKeyRef to the chart Secret.
	envOp := findOp(res.Patch, "add", base+"/containers/0/env")
	if envOp == nil {
		t.Fatalf("no env add op; ops=%+v", res.Patch)
	}
	if !containsJSON(envOp.Value, "PING_KEY_BACKUP") ||
		!containsJSON(envOp.Value, "backups-pingkeys") ||
		!containsJSON(envOp.Value, "secretKeyRef") {
		t.Fatalf("env shape wrong: %s", toJSON(envOp.Value))
	}
	// capture-output default → no privacy env emitted.
	if containsJSON(envOp.Value, "CROND_CAPTURE_OUTPUT") {
		t.Fatalf("did not expect privacy env at defaults: %s", toJSON(envOp.Value))
	}

	// shared volume mounted into the job container.
	if findOp(res.Patch, "add", base+"/containers/0/volumeMounts") == nil {
		t.Fatalf("no volumeMounts add op; ops=%+v", res.Patch)
	}

	// idempotency marker stamped.
	if findOp(res.Patch, "add", "/metadata/annotations/crond.io~1injected") == nil {
		t.Fatalf("no injected annotation op; ops=%+v", res.Patch)
	}
}

func TestBuildInjection_NoInjectAnnotation(t *testing.T) {
	raw := cronJobJSON(t, map[string]string{}, map[string]any{
		"name": "backup", "command": []any{"/opt/backup.sh"},
	})
	res, err := BuildInjection(raw, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	if res.Patch != nil {
		t.Fatalf("expected no patch without opt-in; got %+v", res.Patch)
	}
}

func TestBuildInjection_Idempotent(t *testing.T) {
	raw := cronJobJSON(t, map[string]string{
		"crond.io/inject":       "true",
		"crond.io/ping-key-env": "PING_KEY_BACKUP",
		"crond.io/injected":     "ghcr.io/platops-security/crond-agent:0.3.0",
	}, map[string]any{
		"name": "backup", "command": []any{"/opt/backup.sh"},
	})
	res, err := BuildInjection(raw, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	if res.Patch != nil {
		t.Fatalf("already-injected CronJob must not be re-patched; got %+v", res.Patch)
	}
}

func TestBuildInjection_MissingPingKeyEnv(t *testing.T) {
	raw := cronJobJSON(t, map[string]string{"crond.io/inject": "true"}, map[string]any{
		"name": "backup", "command": []any{"/opt/backup.sh"},
	})
	res, err := BuildInjection(raw, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	if res.Patch != nil {
		t.Fatalf("expected skip without ping-key-env; got %+v", res.Patch)
	}
	if res.Warning == "" {
		t.Fatal("expected a warning explaining the skip")
	}
}

func TestBuildInjection_NoCommand(t *testing.T) {
	raw := cronJobJSON(t, map[string]string{
		"crond.io/inject":       "true",
		"crond.io/ping-key-env": "PING_KEY_BACKUP",
	}, map[string]any{
		"name": "backup", "image": "myco/backup:1.0", // no command
	})
	res, err := BuildInjection(raw, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	if res.Patch != nil {
		t.Fatalf("cannot wrap a container without an explicit command; got %+v", res.Patch)
	}
	if res.Warning == "" {
		t.Fatal("expected a warning explaining the missing command")
	}
}

func TestBuildInjection_Prebaked(t *testing.T) {
	raw := cronJobJSON(t, map[string]string{
		"crond.io/inject":       "true",
		"crond.io/ping-key-env": "PING_KEY_BACKUP",
		"crond.io/mode":         "prebaked",
	}, map[string]any{
		"name": "backup", "image": "myco/backup-with-agent:1.0",
		"command": []any{"/opt/backup.sh"},
	})
	res, err := BuildInjection(raw, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	// no init container / volume / volumeMount in prebaked mode.
	if findOp(res.Patch, "add", base+"/initContainers") != nil {
		t.Fatal("prebaked mode must not inject an init container")
	}
	if findOp(res.Patch, "add", base+"/volumes") != nil {
		t.Fatal("prebaked mode must not add the shared volume")
	}
	// command runs the on-PATH binary, not the shared-mount one.
	cmdOp := findOp(res.Patch, "replace", base+"/containers/0/command")
	if cmdOp == nil {
		t.Fatalf("no command op; ops=%+v", res.Patch)
	}
	first, _ := firstString(cmdOp.Value)
	if first != "crond-agent" {
		t.Fatalf("prebaked command should invoke bare crond-agent, got %q", first)
	}
}

func TestBuildInjection_PrivacyEnv(t *testing.T) {
	cfg := testConfig()
	cfg.CaptureOutput = false
	cfg.RedactPatterns = []string{`Bearer [A-Za-z0-9._-]+`}
	raw := cronJobJSON(t, map[string]string{
		"crond.io/inject":       "true",
		"crond.io/ping-key-env": "PING_KEY_BACKUP",
	}, map[string]any{
		"name": "backup", "command": []any{"/opt/backup.sh"},
	})
	res, err := BuildInjection(raw, cfg)
	if err != nil {
		t.Fatal(err)
	}
	envOp := findOp(res.Patch, "add", base+"/containers/0/env")
	if envOp == nil || !containsJSON(envOp.Value, "CROND_CAPTURE_OUTPUT") ||
		!containsJSON(envOp.Value, "CROND_REDACT_PATTERNS") {
		t.Fatalf("expected privacy env vars; got %s", toJSON(envOp.Value))
	}
}

// --- small JSON helpers (test-only) ---

func toJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func jsonEqual(a, b any) bool { return toJSON(a) == toJSON(b) }

func containsJSON(v any, sub string) bool {
	return len(sub) > 0 && json.Valid([]byte(toJSON(v))) && contains(toJSON(v), sub)
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func firstString(v any) (string, bool) {
	arr, ok := v.([]any)
	if !ok || len(arr) == 0 {
		return "", false
	}
	s, ok := arr[0].(string)
	return s, ok
}

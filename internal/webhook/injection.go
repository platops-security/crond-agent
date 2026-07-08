// Package webhook implements the crond-agent Kubernetes mutating admission
// webhook (V2 auto-injection). It rewrites opt-in CronJobs at admission time to
// run their command through `crond-agent exec` — the same runtime shape the
// V1 `crond-agent.wrap` Helm macro produces, but without editing each spec.
//
// The package is intentionally dependency-free (standard library only): it
// models just the subset of the CronJob it needs and emits an RFC-6902 JSON
// Patch. This keeps the module's dependency + vulnerability surface minimal,
// matching the rest of crond-agent.
package webhook

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Annotation keys read from a CronJob to control injection.
const (
	// AnnInject opts a CronJob in. Must equal "true".
	AnnInject = "crond.io/inject"
	// AnnPingKeyEnv names the env var (and Secret key) holding the monitor's
	// ping key, e.g. "PING_KEY_BACKUP". Required.
	AnnPingKeyEnv = "crond.io/ping-key-env"
	// AnnContainer optionally names the container to wrap (defaults to the first).
	AnnContainer = "crond.io/container"
	// AnnMode optionally selects "prebaked" (image already contains the agent).
	AnnMode = "crond.io/mode"
	// AnnInjected is stamped by the webhook as an idempotency + audit marker.
	AnnInjected = "crond.io/injected"
)

const installerName = "crond-agent-installer"

// InjectorConfig is the injection template, loaded at startup from a mounted
// ConfigMap the Helm chart renders from the SAME values V1's macro uses — so
// the webhook produces the same wrapping as `crond-agent.wrap`.
type InjectorConfig struct {
	AgentImage               string         `yaml:"agentImage"`
	ImagePullPolicy          string         `yaml:"imagePullPolicy"`
	APIURL                   string         `yaml:"apiUrl"`
	ExtraArgs                []string       `yaml:"extraArgs"`
	SharedVolumeName         string         `yaml:"sharedVolumeName"`
	SharedMountPath          string         `yaml:"sharedMountPath"`
	PingKeysSecret           string         `yaml:"pingKeysSecret"`
	CaptureOutput            bool           `yaml:"captureOutput"`
	RedactPatterns           []string       `yaml:"redactPatterns"`
	ContainerSecurityContext map[string]any `yaml:"containerSecurityContext"`
	InitResources            map[string]any `yaml:"initResources"`
}

// PatchOp is a single RFC-6902 JSON Patch operation.
type PatchOp struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
}

// Result is the outcome of an injection decision.
type Result struct {
	// Patch is the JSON Patch to apply, or nil when nothing should change
	// (not opted in, already injected, or skipped).
	Patch []PatchOp
	// Warning, when non-empty, explains why an opted-in CronJob was NOT
	// wrapped; the caller should surface it in the AdmissionResponse.
	Warning string
}

// minimal CronJob shape — only the fields injection reads.
type cronJob struct {
	Metadata struct {
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
	Spec struct {
		JobTemplate struct {
			Spec struct {
				Template struct {
					Spec podSpec `json:"spec"`
				} `json:"template"`
			} `json:"spec"`
		} `json:"jobTemplate"`
	} `json:"spec"`
}

type podSpec struct {
	Volumes        []json.RawMessage `json:"volumes"`
	InitContainers []namedContainer  `json:"initContainers"`
	Containers     []container       `json:"containers"`
}

type namedContainer struct {
	Name string `json:"name"`
}

type container struct {
	Name         string            `json:"name"`
	Command      []string          `json:"command"`
	Args         []string          `json:"args"`
	Env          []json.RawMessage `json:"env"`
	VolumeMounts []json.RawMessage `json:"volumeMounts"`
}

const podPath = "/spec/jobTemplate/spec/template/spec"

// BuildInjection decides whether and how to wrap a CronJob. It returns a nil
// patch when the CronJob is not opted in or is already injected. When opted in
// but not wrappable (missing ping-key-env, no target container, or the target
// container has no explicit command), it returns a nil patch and a Warning.
func BuildInjection(rawCronJob []byte, cfg InjectorConfig) (Result, error) {
	var cj cronJob
	if err := json.Unmarshal(rawCronJob, &cj); err != nil {
		return Result{}, fmt.Errorf("decode CronJob: %w", err)
	}

	ann := cj.Metadata.Annotations
	if ann[AnnInject] != "true" {
		return Result{}, nil // not opted in
	}
	if ann[AnnInjected] != "" {
		return Result{}, nil // idempotent: already wrapped
	}

	ps := cj.Spec.JobTemplate.Spec.Template.Spec
	// Idempotency guard against an already-present installer (e.g. a
	// V1-macro-wrapped CronJob that also matches the webhook).
	for _, ic := range ps.InitContainers {
		if ic.Name == installerName {
			return Result{}, nil
		}
	}

	envKey := ann[AnnPingKeyEnv]
	if envKey == "" {
		return Result{Warning: fmt.Sprintf("crond.io/inject set but %s missing — skipped", AnnPingKeyEnv)}, nil
	}

	idx := targetContainerIndex(ps.Containers, ann[AnnContainer])
	if idx < 0 {
		return Result{Warning: "no matching container to wrap — skipped"}, nil
	}
	target := ps.Containers[idx]
	if len(target.Command) == 0 {
		return Result{Warning: fmt.Sprintf("container %q has no explicit command; set command or use the pre-baked image path — skipped", target.Name)}, nil
	}

	prebaked := ann[AnnMode] == "prebaked"
	ops := make([]PatchOp, 0, 8)

	if !prebaked {
		// shared emptyDir volume.
		vol := map[string]any{"name": cfg.SharedVolumeName, "emptyDir": map[string]any{}}
		ops = append(ops, addToArray(podPath+"/volumes", len(ps.Volumes) == 0, vol))
		// installer init container.
		ops = append(ops, addToArray(podPath+"/initContainers", len(ps.InitContainers) == 0, cfg.initContainer()))
		// mount the shared volume into the job container.
		mount := map[string]any{"name": cfg.SharedVolumeName, "mountPath": cfg.SharedMountPath}
		ops = append(ops, addToArray(containerSub(idx, "volumeMounts"), len(target.VolumeMounts) == 0, mount))
	}

	// rewrite the command to run through the agent.
	ops = append(ops, PatchOp{
		Op:    "replace",
		Path:  containerSub(idx, "command"),
		Value: cfg.wrappedCommand(envKey, prebaked, target.Command, target.Args),
	})
	// args were folded into the command list.
	if len(target.Args) > 0 {
		ops = append(ops, PatchOp{Op: "remove", Path: containerSub(idx, "args")})
	}

	// env: the ping key (secretKeyRef) plus any privacy vars.
	envs := []any{cfg.pingKeyEnv(envKey)}
	envs = append(envs, cfg.privacyEnv()...)
	if len(target.Env) == 0 {
		ops = append(ops, PatchOp{Op: "add", Path: containerSub(idx, "env"), Value: envs})
	} else {
		for _, e := range envs {
			ops = append(ops, PatchOp{Op: "add", Path: containerSub(idx, "env") + "/-", Value: e})
		}
	}

	// idempotency marker (annotations map exists — it holds crond.io/inject).
	ops = append(ops, PatchOp{
		Op:    "add",
		Path:  "/metadata/annotations/" + escapeJSONPointer(AnnInjected),
		Value: cfg.AgentImage,
	})

	return Result{Patch: ops}, nil
}

// targetContainerIndex returns the index of the container to wrap: the one
// named by the annotation, or the first container. Returns -1 if none.
func targetContainerIndex(containers []container, name string) int {
	if len(containers) == 0 {
		return -1
	}
	if name == "" {
		return 0
	}
	for i, c := range containers {
		if c.Name == name {
			return i
		}
	}
	return -1
}

// addToArray builds an add op that either creates the array (whole-array add)
// or appends to it ("/-"), depending on whether it is currently empty.
func addToArray(path string, empty bool, value any) PatchOp {
	if empty {
		return PatchOp{Op: "add", Path: path, Value: []any{value}}
	}
	return PatchOp{Op: "add", Path: path + "/-", Value: value}
}

func containerSub(idx int, field string) string {
	return podPath + "/containers/" + strconv.Itoa(idx) + "/" + field
}

// wrappedCommand builds the rewritten command list. In prebaked mode the agent
// is expected on PATH; otherwise it is the binary the init container installed
// into the shared mount.
func (c InjectorConfig) wrappedCommand(envKey string, prebaked bool, cmd, args []string) []any {
	bin := "crond-agent"
	if !prebaked {
		bin = c.SharedMountPath + "/crond-agent"
	}
	out := []any{
		bin, "exec",
		fmt.Sprintf("--key=$(%s)", envKey),
		"--api-url=" + c.APIURL,
	}
	for _, a := range c.ExtraArgs {
		out = append(out, a)
	}
	out = append(out, "--")
	for _, a := range cmd {
		out = append(out, a)
	}
	for _, a := range args {
		out = append(out, a)
	}
	return out
}

func (c InjectorConfig) initContainer() map[string]any {
	ic := map[string]any{
		"name":            installerName,
		"image":           c.AgentImage,
		"imagePullPolicy": c.ImagePullPolicy,
		"command":         []any{"/crond-agent"},
		"args":            []any{"install", "--target=" + c.SharedMountPath + "/crond-agent"},
		"volumeMounts": []any{
			map[string]any{"name": c.SharedVolumeName, "mountPath": c.SharedMountPath},
		},
	}
	if c.ContainerSecurityContext != nil {
		ic["securityContext"] = c.ContainerSecurityContext
	}
	if c.InitResources != nil {
		ic["resources"] = c.InitResources
	}
	return ic
}

func (c InjectorConfig) pingKeyEnv(envKey string) map[string]any {
	return map[string]any{
		"name": envKey,
		"valueFrom": map[string]any{
			"secretKeyRef": map[string]any{
				"name": c.PingKeysSecret,
				"key":  envKey,
			},
		},
	}
}

// privacyEnv mirrors the chart's crond-agent.privacyEnv macro: emit
// CROND_CAPTURE_OUTPUT only when capture is disabled, and CROND_REDACT_PATTERNS
// only when patterns are set — so defaults stay clean.
func (c InjectorConfig) privacyEnv() []any {
	var out []any
	if !c.CaptureOutput {
		out = append(out, map[string]any{"name": "CROND_CAPTURE_OUTPUT", "value": "false"})
	}
	if len(c.RedactPatterns) > 0 {
		out = append(out, map[string]any{
			"name":  "CROND_REDACT_PATTERNS",
			"value": strings.Join(c.RedactPatterns, "\n"),
		})
	}
	return out
}

// escapeJSONPointer escapes a JSON Pointer reference token per RFC 6901
// (~ → ~0, / → ~1) so annotation keys like "crond.io/injected" are valid.
func escapeJSONPointer(tok string) string {
	tok = strings.ReplaceAll(tok, "~", "~0")
	tok = strings.ReplaceAll(tok, "/", "~1")
	return tok
}

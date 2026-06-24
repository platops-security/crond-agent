package main

import (
	"reflect"
	"testing"
)

func TestExtractConfigFlag_LongForm(t *testing.T) {
	in := []string{"--config", "/etc/crond.yaml", "exec", "--", "echo", "hi"}
	path, rest := extractConfigFlag(in)
	if path != "/etc/crond.yaml" {
		t.Errorf("path = %q, want /etc/crond.yaml", path)
	}
	want := []string{"exec", "--", "echo", "hi"}
	if !reflect.DeepEqual(rest, want) {
		t.Errorf("rest = %v, want %v", rest, want)
	}
}

func TestExtractConfigFlag_EqualsForm(t *testing.T) {
	in := []string{"--config=/etc/crond.yaml", "ping"}
	path, rest := extractConfigFlag(in)
	if path != "/etc/crond.yaml" {
		t.Errorf("path = %q, want /etc/crond.yaml", path)
	}
	want := []string{"ping"}
	if !reflect.DeepEqual(rest, want) {
		t.Errorf("rest = %v, want %v", rest, want)
	}
}

func TestExtractConfigFlag_Missing(t *testing.T) {
	in := []string{"exec", "--", "echo", "hi"}
	path, rest := extractConfigFlag(in)
	if path != "" {
		t.Errorf("path = %q, want empty", path)
	}
	if !reflect.DeepEqual(rest, in) {
		t.Errorf("rest should equal input when no --config present")
	}
}

// TestExtractConfigFlag_DoesNotMutateInput pins the no-mutation contract.
// Earlier impl used `append(args[:i], args[i+2:]...)` which overwrites the
// underlying array — surprising for any caller that retains a reference
// (os.Args!), and a future bug magnet.
func TestExtractConfigFlag_DoesNotMutateInput(t *testing.T) {
	in := []string{"--config", "/etc/crond.yaml", "exec", "--", "echo", "hi"}
	original := append([]string(nil), in...) // snapshot
	_, _ = extractConfigFlag(in)
	if !reflect.DeepEqual(in, original) {
		t.Errorf("extractConfigFlag mutated its input slice:\n got %v\nwant %v", in, original)
	}
}

func TestExtractConfigFlag_StopsAtSubcommand(t *testing.T) {
	// A `--config` appearing after the subcommand belongs to that
	// subcommand, not to the global agent flag set.
	in := []string{"exec", "--config", "/should-be-passed-through", "--", "echo"}
	path, rest := extractConfigFlag(in)
	if path != "" {
		t.Errorf("path = %q, want empty (--config is after subcommand)", path)
	}
	if !reflect.DeepEqual(rest, in) {
		t.Errorf("rest = %v, want %v", rest, in)
	}
}

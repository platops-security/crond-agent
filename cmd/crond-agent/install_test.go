package main

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallSelf_CopiesBytesAndSetsExecutableMode(t *testing.T) {
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "fake-binary")
	payload := []byte("\x7fELF" + "fake-binary-content")
	if err := os.WriteFile(src, payload, 0o644); err != nil {
		t.Fatalf("seed source: %v", err)
	}

	dstDir := t.TempDir()
	dst := filepath.Join(dstDir, "crond-agent")

	n, err := installSelf(src, dst)
	if err != nil {
		t.Fatalf("installSelf: %v", err)
	}
	if n != len(payload) {
		t.Errorf("returned n=%d, want %d", n, len(payload))
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if sha256.Sum256(got) != sha256.Sum256(payload) {
		t.Errorf("dst contents differ from src")
	}

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat dst: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("dst mode = %o, want 0755 (needed so main container can exec it)", info.Mode().Perm())
	}
}

func TestInstallSelf_ReturnsErrorOnMissingSource(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "out")
	_, err := installSelf(filepath.Join(t.TempDir(), "does-not-exist"), dst)
	if err == nil {
		t.Fatal("expected error for missing source, got nil")
	}
}

func TestInstallSelf_ReturnsErrorOnUnwritableTarget(t *testing.T) {
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "src")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Target's parent directory doesn't exist.
	dst := filepath.Join(t.TempDir(), "missing-dir", "out")
	_, err := installSelf(src, dst)
	if err == nil {
		t.Fatal("expected error writing to nonexistent parent dir, got nil")
	}
}

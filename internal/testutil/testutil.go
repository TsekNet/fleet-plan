// Package testutil provides shared test helpers for fleet-plan packages.
package testutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestdataRoot returns the absolute path to the shared testdata/ fixture repo.
// It uses runtime.Caller to locate the caller's file, then walks up to the
// repo root and appends "testdata".
func TestdataRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(1)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	// Walk up from the calling file to find the repo root (contains go.mod).
	dir := filepath.Dir(thisFile)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root (no go.mod found)")
		}
		dir = parent
	}
	root := filepath.Join(dir, "testdata")
	abs, err := filepath.Abs(root)
	if err != nil {
		t.Fatalf("resolving testdata root: %v", err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("testdata directory not found at %s: %v", abs, err)
	}
	return abs
}

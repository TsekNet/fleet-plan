package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// ---------- version command ----------

func TestVersionCommand(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	root := buildRootCmd()
	root.SetArgs([]string{"version"})
	err := root.Execute()

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	os.Stdout = old

	if err != nil {
		t.Fatalf("version command error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "fleet-plan") {
		t.Errorf("version output should contain 'fleet-plan', got:\n%s", output)
	}
}

// ---------- global flags ----------

func TestGlobalFlags(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		check func(t *testing.T)
	}{
		{
			name: "format flag",
			args: []string{"--format", "json", "version"},
			check: func(t *testing.T) {
				if flagFormat != "json" {
					t.Errorf("flagFormat: got %q, want json", flagFormat)
				}
			},
		},
		{
			name: "repo flag",
			args: []string{"--repo", "/custom/path", "version"},
			check: func(t *testing.T) {
				if flagRepo != "/custom/path" {
					t.Errorf("flagRepo: got %q", flagRepo)
				}
			},
		},
		{
			name: "no-color flag",
			args: []string{"--no-color", "version"},
			check: func(t *testing.T) {
				if !flagNoColor {
					t.Error("flagNoColor should be true")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagRepo = "."
			flagFormat = "terminal"
			flagNoColor = false
			flagVerbose = false

			old := os.Stdout
			r, w, _ := os.Pipe()
			os.Stdout = w

			root := buildRootCmd()
			root.SetArgs(tt.args)
			root.Execute()

			w.Close()
			var buf bytes.Buffer
			buf.ReadFrom(r)
			os.Stdout = old

			tt.check(t)
		})
	}
}

// ---------- root flags include --team, --default, --verbose ----------

func TestRootFlagsIncludeAllFlags(t *testing.T) {
	root := buildRootCmd()
	root.SetArgs([]string{"--help"})

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := root.Execute()

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	os.Stdout = old

	if err != nil {
		t.Fatalf("--help error: %v", err)
	}

	output := buf.String()
	for _, flag := range []string{"--team", "--default", "--verbose"} {
		if !strings.Contains(output, flag) {
			t.Errorf("help should mention %s, got:\n%s", flag, output)
		}
	}
}

// ---------- requires auth ----------

func TestRequiresAuth(t *testing.T) {
	t.Setenv("FLEET_PLAN_URL", "")
	t.Setenv("FLEET_PLAN_TOKEN", "")
	t.Setenv("HOME", t.TempDir())

	root := buildRootCmd()
	root.SetArgs([]string{"--repo", t.TempDir()})
	err := root.Execute()
	if err == nil {
		t.Error("expected error when auth is missing")
	}
	if !strings.Contains(err.Error(), "URL required") {
		t.Errorf("expected URL required error, got: %v", err)
	}
}

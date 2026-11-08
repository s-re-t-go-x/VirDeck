package config

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddedDefaultsRejectInvalidData(t *testing.T) {
	var raw map[string]any
	if err := json.Unmarshal(builtin, &raw); err != nil {
		t.Fatal(err)
	}
	raw["create"].(map[string]any)["memoryMB"] = float64(-1)
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	var d Defaults
	if err := decode(encoded, &d); err == nil {
		t.Fatal("invalid compiled default memory accepted")
	}
}

func TestExecutableUsesEmbeddedDefaultsOnly(t *testing.T) {
	if os.Getenv("VIRDECK_DEFAULTS_HELPER") == "1" {
		if LoadError != nil {
			t.Fatal(LoadError)
		}
		fmt.Printf("compiled-memory=%d\n", Values.Create.MemoryMB)
		return
	}
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		files map[string]string
	}{
		{name: "standalone"},
		{name: "adjacent external file ignored", files: map[string]string{"dist/config/defaults.json": `{"create":{"memoryMB":4096}}`}},
		{name: "invalid external file ignored", files: map[string]string{"dist/config/defaults.json": `invalid json`}},
		{name: "project defaults ignored", files: map[string]string{"go.mod": "module virdeck\n", "config/defaults.json": `{"create":{"memoryMB":8192}}`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, "dist"), 0755); err != nil {
				t.Fatal(err)
			}
			exe := filepath.Join(dir, "dist", "config-check.exe")
			if err := os.WriteFile(exe, binary, 0755); err != nil {
				t.Fatal(err)
			}
			for rel, content := range tc.files {
				path := filepath.Join(dir, rel)
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(exe, "-test.run=^TestExecutableUsesEmbeddedDefaultsOnly$")
			cmd.Env = append(os.Environ(), "VIRDECK_DEFAULTS_HELPER=1")
			out, err := cmd.CombinedOutput()
			want := fmt.Sprintf("compiled-memory=%d", Values.Create.MemoryMB)
			if err != nil || !strings.Contains(string(out), want) {
				t.Fatalf("runtime read external defaults: %s, %v", out, err)
			}
		})
	}
}

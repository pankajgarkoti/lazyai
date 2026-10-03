package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaterializeOpenCodeUsesV2(t *testing.T) {
	for _, backend := range []string{"opencode"} {
		dir := filepath.Join(t.TempDir(), backend)
		if err := os.MkdirAll(filepath.Join(dir, "plugins"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "plugins/lazyai.ts"), []byte("export default async () => ({})"), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := MaterializeFor(dir, backend); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(dir, "plugins/lazyai.ts"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "@opencode/plugin") || strings.Contains(string(data), "@opencode-ai/plugin") {
			t.Fatalf("%s got wrong plugin API", backend)
		}
		if _, err := os.Stat(filepath.Join(dir, "skills/lazyai-show/SKILL.md")); err != nil {
			t.Fatal(err)
		}
	}
}

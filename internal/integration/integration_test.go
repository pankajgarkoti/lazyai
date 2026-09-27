package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaterializeSeparateOpenCodeVersions(t *testing.T) {
	for _, backend := range []string{"opencode", "opencode2"} {
		dir := filepath.Join(t.TempDir(), backend)
		if _, err := MaterializeFor(dir, backend); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(dir, "plugins/lazyai.ts"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), map[string]string{"opencode": "@opencode-ai/plugin", "opencode2": "@opencode/plugin"}[backend]) {
			t.Fatalf("%s got wrong plugin API", backend)
		}
		if _, err := os.Stat(filepath.Join(dir, "skills/lazyai-show/SKILL.md")); err != nil {
			t.Fatal(err)
		}
	}
}

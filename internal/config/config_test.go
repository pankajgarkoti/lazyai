package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root, body string) {
	t.Helper()
	dir := filepath.Join(root, Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, File), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const valid = `
version: 1
interactive:
  strict: true
  default_contract: task
  contracts:
    task:
      title: Task contract
      fields:
        - key: outcome
          label: Outcome
          type: multiline
          required: true
        - key: acceptance
          label: Acceptance criteria
          type: multiline
          required: true
        - key: constraints
          label: Constraints
          type: text
`

func TestMissingFileIsNotAnErrorAndPreservesDefaults(t *testing.T) {
	cfg, warnings, err := Load(t.TempDir())
	if err != nil || len(warnings) != 0 {
		t.Fatalf("err=%v warnings=%v", err, warnings)
	}
	if cfg.Interactive.Strict || cfg.Loaded {
		t.Fatalf("missing config must not enable strict: %+v", cfg)
	}
	if _, ok := cfg.Contract(); ok {
		t.Fatal("no contract without config")
	}
}

func TestValidConfigParsesTemplatesInOrder(t *testing.T) {
	root := t.TempDir()
	write(t, root, valid)
	cfg, warnings, err := Load(root)
	if err != nil || len(warnings) != 0 {
		t.Fatalf("err=%v warnings=%v", err, warnings)
	}
	if !cfg.Loaded || !cfg.Interactive.Strict || cfg.Interactive.DefaultContract != "task" {
		t.Fatalf("cfg=%+v", cfg)
	}
	c, ok := cfg.Contract()
	if !ok || c.Name != "task" || c.Title != "Task contract" || len(c.Fields) != 3 {
		t.Fatalf("contract=%+v ok=%v", c, ok)
	}
	if c.Fields[0].Key != "outcome" || c.Fields[0].Type != FieldMultiline || !c.Fields[0].Required {
		t.Fatalf("field0=%+v", c.Fields[0])
	}
	if c.Fields[2].Type != FieldText || c.Fields[2].Required {
		t.Fatalf("field2 defaults: %+v", c.Fields[2])
	}
}

func TestUnknownTopLevelKeysWarnButLoad(t *testing.T) {
	root := t.TempDir()
	write(t, root, valid+"\nfuture_feature:\n  enabled: true\n")
	cfg, warnings, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "future_feature") {
		t.Fatalf("warnings=%v", warnings)
	}
	if !cfg.Interactive.Strict {
		t.Fatal("warning must not disable the config")
	}
}

func TestInvalidConfigsFailVisiblyAndNeverEnforce(t *testing.T) {
	cases := map[string]string{
		"malformed yaml":        "version: 1\ninteractive: [\n",
		"unsupported version":   strings.Replace(valid, "version: 1", "version: 2", 1),
		"missing version":       strings.Replace(valid, "version: 1\n", "", 1),
		"duplicate field key":   strings.Replace(valid, "key: constraints", "key: outcome", 1),
		"unsupported type":      strings.Replace(valid, "type: text", "type: markdown", 1),
		"missing label":         strings.Replace(valid, "          label: Constraints\n", "", 1),
		"missing key":           strings.Replace(valid, "        - key: outcome\n          label: Outcome", "        - label: Outcome", 1),
		"default not defined":   strings.Replace(valid, "default_contract: task", "default_contract: nope", 1),
		"strict with no fields": strings.Replace(valid, "      fields:", "      fields: []\n      ignored:", 1),
	}
	for name, body := range cases {
		root := t.TempDir()
		write(t, root, body)
		cfg, _, err := Load(root)
		if err == nil {
			t.Errorf("%s: expected an error", name)
			continue
		}
		if cfg.Interactive.Strict {
			t.Errorf("%s: invalid config must not enforce strict mode", name)
		}
		if _, ok := cfg.Contract(); ok {
			t.Errorf("%s: invalid config must not expose a contract", name)
		}
	}
}

func TestStrictFalseWithTemplatesIsValidAndOptional(t *testing.T) {
	root := t.TempDir()
	write(t, root, strings.Replace(valid, "strict: true", "strict: false", 1))
	cfg, _, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Interactive.Strict {
		t.Fatal("strict should be off")
	}
	if _, ok := cfg.Contract(); !ok {
		t.Fatal("templates remain available for opt-in use")
	}
}

func TestRenderIsDeterministicAndSkipsEmptyOptionalFields(t *testing.T) {
	c := Contract{Name: "task", Fields: []Field{
		{Key: "outcome", Type: FieldMultiline, Required: true},
		{Key: "acceptance", Type: FieldMultiline, Required: true},
		{Key: "constraints", Type: FieldText},
	}}
	values := map[string]string{
		"outcome":     "Ship it\nwith tests",
		"acceptance":  "  go test passes  ",
		"constraints": "",
	}
	got := c.Render(values)
	want := "contract: task\noutcome: |\n  Ship it\n  with tests\nacceptance: |\n  go test passes\n"
	if got != want {
		t.Fatalf("render:\n%q\nwant\n%q", got, want)
	}
	if again := c.Render(values); again != got {
		t.Fatal("render must be deterministic")
	}
	missing := c.Missing(map[string]string{"outcome": "x"})
	if len(missing) != 1 || missing[0] != "acceptance" {
		t.Fatalf("missing=%v", missing)
	}
	// Control characters never reach the child terminal as raw bytes.
	if r := c.Render(map[string]string{"outcome": "a\x1bb\x11c", "acceptance": "ok"}); strings.ContainsAny(r, "\x1b\x11") {
		t.Fatalf("control bytes leaked: %q", r)
	}
}

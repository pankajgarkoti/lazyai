package config

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"
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

func TestMissingFileCreatesExplicitDefaults(t *testing.T) {
	root := t.TempDir()
	cfg, warnings, err := Load(root)
	if err != nil || len(warnings) != 0 {
		t.Fatalf("err=%v warnings=%v", err, warnings)
	}
	if cfg.Interactive.Strict || !cfg.Loaded || cfg.Path != Path(root) || cfg.Version != Version {
		t.Fatalf("created config must load without enabling strict: %+v", cfg)
	}
	if c, ok := cfg.Contract(); !ok || c.Name != "task" {
		t.Fatalf("default contract=%+v ok=%v", c, ok)
	}
	data, err := os.ReadFile(Path(root))
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Version     *int `yaml:"version"`
		Interactive struct {
			Strict          *bool  `yaml:"strict"`
			DefaultContract string `yaml:"default_contract"`
			Contracts       map[string]struct {
				Title  string `yaml:"title"`
				Fields []struct {
					Key      string `yaml:"key"`
					Label    string `yaml:"label"`
					Type     string `yaml:"type"`
					Required *bool  `yaml:"required"`
				} `yaml:"fields"`
			} `yaml:"contracts"`
		} `yaml:"interactive"`
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&raw); err != nil {
		t.Fatal(err)
	}
	if raw.Version == nil || raw.Interactive.Strict == nil || *raw.Interactive.Strict || raw.Interactive.DefaultContract != "task" {
		t.Fatal("top-level and interactive options must be explicit")
	}
	// Minimum inputs distinguish workflow phases without mirroring every label or optional field.
	minimums := map[string]string{
		"task":                  "outcome acceptance",
		"system_mapping":        "target question",
		"environment_forensics": "environment locator window question authority",
		"behavior_review":       "environment window population question authority",
		"incident_triage":       "reports expected",
		"incident_rca":          "incident environment evidence expected authority",
		"blast_radius":          "target change preserve",
		"change_design":         "outcome acceptance",
		"implementation":        "outcome acceptance authority",
		"verification":          "revision environment acceptance authority",
		"release":               "revision environment evidence authority recovery",
		"incident_response":     "environment impact evidence authority recovery",
		"outcome_review":        "target outcome evidence",
	}
	if len(raw.Interactive.Contracts) != len(minimums) {
		t.Errorf("want %d workflow templates, got %d", len(minimums), len(raw.Interactive.Contracts))
	}
	for name, minimum := range minimums {
		t.Run(name, func(t *testing.T) {
			c := raw.Interactive.Contracts[name]
			if c.Title == "" || len(c.Fields) < 3 || len(c.Fields) > 6 {
				t.Fatalf("%s needs a title and useful fields: %+v", name, c)
			}
			required := map[string]bool{}
			optional := 0
			for _, f := range c.Fields {
				if f.Key == "" || f.Label == "" || f.Type == "" || f.Required == nil {
					t.Fatalf("%s field options must be explicit: %+v", name, f)
				}
				wantType := "multiline"
				switch f.Key {
				case "target", "environment", "locator", "window", "revision", "incident":
					wantType = "text"
				}
				if f.Type != wantType {
					t.Errorf("%s.%s type=%s, want %s", name, f.Key, f.Type, wantType)
				}
				required[f.Key] = *f.Required
				if !*f.Required {
					optional++
				}
				if f.Key == "authority" {
					promptType := "permit"
					if name == "environment_forensics" || name == "behavior_review" {
						promptType = "reads"
					}
					for _, prompt := range []string{promptType, "stops"} {
						if !strings.Contains(strings.ToLower(f.Label), prompt) {
							t.Errorf("%s authority label must solicit %q", name, prompt)
						}
					}
				}
			}
			// Critical phase boundaries must survive in the visible form, not just comments.
			phaseHints := map[string][]string{
				"system_mapping":        {"Explain only", "No design/edits"},
				"environment_forensics": {"Facts, not RCA", "Reads + stops"},
				"behavior_review":       {"Review, no RCA", "Reads + stops"},
				"incident_triage":       {"No RCA/edits"},
				"incident_rca":          {"No fix/release"},
				"blast_radius":          {"No prod edits"},
				"change_design":         {"Design goal", "No code edits"},
				"implementation":        {"Approved goal", "Tests; no ship"},
				"verification":          {"No fix/release"},
				"release":               {"Verified ref", "Recovery/check"},
				"incident_response":     {"Recovery check", "No broader fixes"},
				"outcome_review":        {"No mutations"},
			}
			labels := ""
			for _, f := range c.Fields {
				labels += f.Label + "\n"
			}
			for _, hint := range phaseHints[name] {
				if !strings.Contains(labels, hint) {
					t.Errorf("missing visible phase hint %q", hint)
				}
			}
			if name == "task" && (len(c.Fields) != 3 || c.Fields[0].Key != "outcome" || c.Fields[1].Key != "acceptance" || c.Fields[2].Key != "constraints") {
				t.Fatal("preserve task fields and order")
			}
			if optional == 0 {
				t.Error("keep supporting evidence, checks, or constraints optional")
			}
			for _, key := range strings.Fields(minimum) {
				if !required[key] {
					t.Errorf("%s requires %s", name, key)
				}
			}
			// Every shipped template must work when selected and opted into.
			enabled := strings.Replace(string(data), "strict: false", "strict: true", 1)
			enabled = strings.Replace(enabled, "default_contract: task", "default_contract: "+name, 1)
			parsed, _, err := Parse([]byte(enabled))
			if err != nil || !parsed.Interactive.Strict {
				t.Fatalf("%s opt-in: cfg=%+v err=%v", name, parsed, err)
			}
			if selected, ok := parsed.Contract(); !ok || selected.Name != name {
				t.Fatalf("%s selection: %+v", name, selected)
			}
		})
	}
	info, err := os.Stat(Path(root))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("new config permissions: info=%v err=%v", info, err)
	}
}

func TestLoadPreservesExistingFilesExactly(t *testing.T) {
	for name, body := range map[string]string{"valid": valid, "commented": "# user comment\n" + valid, "invalid": "version: [", "empty": ""} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			write(t, root, body)
			if err := os.Chmod(Path(root), 0o400); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(Path(root))
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				Load(root)
			}
			after, err := os.Stat(Path(root))
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(Path(root))
			if err != nil || string(data) != body || !os.SameFile(before, after) || before.Mode() != after.Mode() || before.ModTime() != after.ModTime() {
				t.Fatalf("existing config changed: data=%q err=%v", data, err)
			}
		})
	}
}

func TestConcurrentLoadCreatesOnlyCompleteDefaults(t *testing.T) {
	root := t.TempDir()
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 32 {
		wg.Go(func() {
			<-start
			cfg, warnings, err := Load(root)
			if err != nil || !cfg.Loaded || cfg.Interactive.Strict || len(warnings) != 0 || len(cfg.Interactive.Contracts) != 13 {
				t.Errorf("concurrent load: cfg=%+v warnings=%v err=%v", cfg, warnings, err)
			}
		})
	}
	close(start)
	wg.Wait()
	entries, err := os.ReadDir(filepath.Join(root, Dir))
	if err != nil || len(entries) != 1 || entries[0].Name() != File {
		t.Fatalf("unexpected files after creation: %v err=%v", entries, err)
	}
}

func TestLoadCreationErrorsNeverEnforce(t *testing.T) {
	for _, kind := range []string{"blocked directory", "read-only directory", "dangling symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, Dir)
			switch kind {
			case "blocked directory":
				if err := os.WriteFile(dir, []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "read-only directory":
				if os.Geteuid() == 0 {
					t.Skip("root bypasses directory permissions")
				}
				if err := os.Mkdir(dir, 0o500); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.Chmod(dir, 0o700) })
			case "dangling symlink":
				if err := os.Mkdir(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("missing-target", Path(root)); err != nil {
					t.Fatal(err)
				}
			}
			cfg, _, err := Load(root)
			if err == nil || cfg.Loaded || cfg.Interactive.Strict {
				t.Fatalf("creation failure must return an error and disable strict: cfg=%+v err=%v", cfg, err)
			}
			if kind == "dangling symlink" {
				if target, err := os.Readlink(Path(root)); err != nil || target != "missing-target" {
					t.Fatalf("symlink changed: target=%q err=%v", target, err)
				}
			}
		})
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

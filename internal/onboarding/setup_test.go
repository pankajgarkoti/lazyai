package onboarding

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lazyai/internal/config"
)

func TestFirstRunCollectsAndPersistsProjectChoices(t *testing.T) {
	root := t.TempDir()
	var output bytes.Buffer
	var selected config.Agent
	err := Run(root, strings.NewReader("2\n/custom/codex\nyes\nimplementation\ny\n"), &output, Options{Validate: func(a config.Agent) error { selected = a; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	cfg, warnings, err := config.Load(root)
	if err != nil || len(warnings) != 0 || cfg.Agent.Backend != "codex" || cfg.Agent.Executable != "/custom/codex" || !cfg.Interactive.Strict || cfg.Interactive.DefaultContract != "implementation" {
		t.Fatalf("cfg=%+v warnings=%v err=%v", cfg, warnings, err)
	}
	if selected != cfg.Agent || len(cfg.Interactive.Contracts) != 7 {
		t.Fatal("validation or templates lost")
	}
	data, _ := os.ReadFile(config.Path(root))
	if !bytes.Contains(data, []byte("# Templates")) {
		t.Fatal("setup lost editable configuration comments")
	}
	before := string(data)
	output.Reset()
	if err := Run(root, strings.NewReader("q\n"), &output, Options{}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(config.Path(root))
	if string(after) != before || output.Len() != 0 {
		t.Fatal("existing configuration was prompted for or overwritten")
	}
}

func TestCancellationAndEOFDoNotInitialize(t *testing.T) {
	for _, input := range []string{"q\n", "2\n", "1\n\nno\ntask\nn\n"} {
		root := t.TempDir()
		err := Run(root, strings.NewReader(input), &bytes.Buffer{}, Options{})
		if !errors.Is(err, ErrCanceled) {
			t.Fatalf("input=%q err=%v", input, err)
		}
		if _, err := os.Stat(filepath.Join(root, config.Dir)); !os.IsNotExist(err) {
			t.Fatalf("cancel created files: %v", err)
		}
	}
}

func TestInvalidChoicesRepromptAndDefaultsAreExplicit(t *testing.T) {
	root := t.TempDir()
	var out bytes.Buffer
	err := Run(root, strings.NewReader("invalid\n\n\nmaybe\n\nmissing_workflow\n\n\n"), &out, Options{})
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.Load(root)
	if err != nil || cfg.Agent.Backend != "opencode" || cfg.Interactive.Strict || cfg.Interactive.DefaultContract != "task" {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
	if !strings.Contains(out.String(), "Choose") {
		t.Fatalf("no validation feedback: %s", out.String())
	}
}

func TestFailedExecutableValidationDoesNotSave(t *testing.T) {
	root := t.TempDir()
	err := Run(root, strings.NewReader("2\n\nno\ntask\ny\nq\n"), &bytes.Buffer{}, Options{Validate: func(config.Agent) error { return errors.New("missing executable") }})
	if !errors.Is(err, ErrCanceled) {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(config.Path(root)); !os.IsNotExist(err) {
		t.Fatal("saved configuration for an unusable executable")
	}
}

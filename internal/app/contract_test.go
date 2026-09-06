package app

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"lazyai/internal/config"
)

func TestShippedContractsAtMinimumSize(t *testing.T) {
	cfg, _, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name := range cfg.Interactive.Contracts {
		t.Run(name, func(t *testing.T) {
			h := strictHarness(t, "")
			selectShippedContract(t, h, name)
			h.update(tea.WindowSizeMsg{Width: 60, Height: 18})
			h.key("i")
			f := h.m.contract
			for i, in := range f.inputs {
				if f.focus != i {
					t.Fatalf("focus=%d, want %d", f.focus, i)
				}
				view := stripANSI(h.m.View())
				want := "› " + in.field.Label
				if in.field.Required {
					want += " *"
				}
				// Match the label row, not the input's duplicate placeholder.
				if !strings.Contains(view, want) {
					t.Errorf("%s full label/required hint %q missing:\n%s", in.field.Key, want, view)
				}
				if lipgloss.Width(view) > 60 || lipgloss.Height(view) > 18 {
					t.Fatalf("unbounded form: %dx%d", lipgloss.Width(view), lipgloss.Height(view))
				}
				h.key("ok")
				if in.value() != "ok" {
					t.Fatalf("%s cannot accept input", in.field.Key)
				}
				h.key("tab")
			}
			h.update(tea.KeyMsg{Type: tea.KeyCtrlS})
			if h.m.contract != nil || h.m.focus != FocusContent || !h.forward[len(h.forward)-1] {
				t.Fatal("filled form did not submit")
			}
			waitScreen(t, h, "ok")
		})
	}
}

func selectShippedContract(t *testing.T, h *harness, name string) {
	t.Helper()
	data, err := os.ReadFile(config.Path(h.root))
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Replace(string(data), "strict: false", "strict: true", 1)
	body = strings.Replace(body, "default_contract: "+h.m.project.Interactive.DefaultContract, "default_contract: "+name, 1)
	if err := os.WriteFile(config.Path(h.root), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	h.update(EscapeMsg{})
	h.update(LeaderMsg{})
	h.key("c")
}

func TestContractDraftIsolation(t *testing.T) {
	h := strictHarness(t, "")
	for _, step := range []struct {
		name, template, want, input string
		stream                      int
		submit                      bool
	}{
		{name: "release draft", template: "release", input: "deploy production"},
		{name: "same template reopens", template: "release", want: "deploy production"},
		{name: "verification has no release authority", template: "verification", input: "read only"},
		{name: "switch back restores release", template: "release", want: "deploy production"},
		{name: "other workstream starts blank", template: "release", stream: 1, input: "staging only"},
		{name: "original workstream retains draft", template: "release", want: "deploy production"},
		{name: "send verification", template: "verification", want: "read only", submit: true},
		{name: "sent template starts blank", template: "verification"},
		{name: "send keeps other template", template: "release", want: "deploy production"},
		{name: "other workstream retains draft", template: "release", stream: 1, want: "staging only"},
	} {
		t.Run(step.name, func(t *testing.T) {
			if step.stream == len(h.m.streams) {
				if _, err := h.m.addStreamOpts(t.TempDir(), "second", "", "", false); err != nil {
					t.Fatal(err)
				}
			}
			h.update(LeaderMsg{})
			h.key(string(rune('1' + step.stream)))
			selectShippedContract(t, h, step.template)
			h.key("i")
			f := h.m.contract
			for i, in := range f.inputs {
				if in.field.Key == "authority" {
					if in.value() != step.want {
						t.Errorf("authority=%q, want %q", in.value(), step.want)
					}
					f.setFocus(i)
					in.setValue("")
					h.key(step.want + step.input)
				}
			}
			if step.submit {
				for _, in := range f.inputs {
					if in.value() == "" {
						in.setValue("ok")
					}
				}
				h.update(tea.KeyMsg{Type: tea.KeyCtrlS})
				if h.m.contract != nil {
					t.Fatal("submission failed")
				}
			} else {
				h.key("esc")
			}
		})
	}
}

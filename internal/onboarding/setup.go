// Package onboarding runs first-launch setup before LazyAI takes terminal raw
// mode or starts any agent. Existing projects and reattachments skip it entirely.
package onboarding

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"lazyai/internal/config"
)

var ErrCanceled = errors.New("project setup canceled")

type Options struct {
	DefaultAgent string
	Executable   string // an explicitly supplied legacy --opencode override
	Validate     func(config.Agent) error
}

func Run(root string, in io.Reader, out io.Writer, opts Options) error {
	needed, err := config.NeedsSetup(root)
	if err != nil || !needed {
		return err
	}
	defaults, err := config.Defaults()
	if err != nil {
		return err
	}
	w := wizard{in: bufio.NewReader(in), out: out}
	fmt.Fprintf(out, "\nWelcome to LazyAI — project setup\nSettings: %s\nPress Enter for defaults; type q to cancel.\n", config.Path(root))
	backend := opts.DefaultAgent
	if backend == "" {
		backend = "opencode"
	}
	for {
		chosen, err := w.choice("Coding agent", []string{"opencode", "codex"}, backend)
		if err != nil {
			return err
		}
		executableDefault := opts.Executable
		if chosen != "opencode" {
			executableDefault = ""
		}
		prompt := "Executable path (blank uses " + chosen + " on PATH)"
		if executableDefault != "" {
			prompt = "Executable path [" + executableDefault + "]"
		}
		executable, err := w.answer(prompt)
		if err != nil {
			return err
		}
		if executable == "" {
			executable = executableDefault
		}
		if strings.HasPrefix(executable, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			executable = filepath.Join(home, strings.TrimPrefix(executable, "~/"))
		}
		fmt.Fprintln(out, "Strict mode opens a structured task form instead of typing directly into the agent.")
		strict, err := w.choice("Enable strict mode", []string{"no", "yes"}, "no")
		if err != nil {
			return err
		}
		fmt.Fprintln(out, "Default workflow selects the initial form when strict mode is enabled:")
		var workflows []string
		for _, c := range defaults.ContractChoices() {
			workflows = append(workflows, c.Name)
			fmt.Fprintf(out, "  %d. %s — %s\n", len(workflows), c.Name, c.Title)
		}
		workflow, err := w.choice("Default workflow", workflows, defaults.Interactive.DefaultContract)
		if err != nil {
			return err
		}
		choices := config.InitialChoices{Agent: config.Agent{Backend: chosen, Executable: executable}, Strict: strict == "yes", DefaultContract: workflow}
		fmt.Fprintf(out, "\nProject: %s\nAgent: %s · strict: %s · workflow: %s\n", root, chosen, strict, workflow)
		if executable != "" {
			fmt.Fprintf(out, "Executable: %s\n", executable)
		}
		confirm, err := w.choice("Save and start", []string{"yes", "no"}, "yes")
		if err != nil {
			return err
		}
		if confirm == "no" {
			return ErrCanceled
		}
		if opts.Validate != nil {
			if err := opts.Validate(choices.Agent); err != nil {
				fmt.Fprintf(out, "Setup not saved: %v\nChoose corrected settings, or q to cancel.\n\n", err)
				continue
			}
		}
		created, err := config.Initialize(root, choices)
		if err != nil {
			return err
		}
		if created {
			fmt.Fprintf(out, "Saved %s\n\n", config.Path(root))
		} else {
			fmt.Fprintln(out, "A configuration was created by another process; using that file.")
		}
		return nil
	}
}

type wizard struct {
	in  *bufio.Reader
	out io.Writer
}

func (w wizard) answer(prompt string) (string, error) {
	fmt.Fprintf(w.out, "%s: ", prompt)
	line, err := w.in.ReadString('\n')
	if errors.Is(err, io.EOF) {
		return "", ErrCanceled
	}
	if err != nil {
		return "", err
	}
	line = strings.TrimSpace(line)
	if strings.EqualFold(line, "q") {
		return "", ErrCanceled
	}
	return line, nil
}

func (w wizard) choice(prompt string, choices []string, def string) (string, error) {
	for {
		value, err := w.answer(fmt.Sprintf("%s (%s) [%s]", prompt, strings.Join(choices, " / "), def))
		if err != nil {
			return "", err
		}
		if value == "" {
			return def, nil
		}
		value = strings.ToLower(value)
		if value == "y" {
			value = "yes"
		}
		if value == "n" {
			value = "no"
		}
		if index, err := strconv.Atoi(value); err == nil && index >= 1 && index <= len(choices) {
			return choices[index-1], nil
		}
		for _, choice := range choices {
			if value == choice {
				return value, nil
			}
		}
		fmt.Fprintf(w.out, "Choose one of: %s (or its number).\n", strings.Join(choices, ", "))
	}
}

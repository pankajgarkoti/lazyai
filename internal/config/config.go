// Package config reads the optional per-project LazyAI configuration at
// <repo>/.lazyai/config.yaml.
//
// Absence of the file preserves every default. Malformed or unsupported
// configuration is reported as an error and results in a Config with strict
// mode disabled and no contract: LazyAI must never silently enforce a
// different contract than the one the user wrote.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// Dir is the project-relative configuration directory.
	Dir = ".lazyai"
	// File is the configuration file name inside Dir.
	File = "config.yaml"
	// Version is the only supported configuration version.
	Version = 1
)

// FieldType is the input widget a contract field uses.
type FieldType string

const (
	FieldText      FieldType = "text"
	FieldMultiline FieldType = "multiline"
)

// Field is one entry in a contract template.
type Field struct {
	Key      string    `yaml:"key"`
	Label    string    `yaml:"label"`
	Type     FieldType `yaml:"type"`
	Required bool      `yaml:"required"`
}

// Contract is a validated template. Name is the map key in the file.
type Contract struct {
	Name   string  `yaml:"-"`
	Title  string  `yaml:"title"`
	Fields []Field `yaml:"fields"`
}

// Interactive configures strict instruction entry.
type Interactive struct {
	Strict          bool                `yaml:"strict"`
	DefaultContract string              `yaml:"default_contract"`
	Contracts       map[string]Contract `yaml:"contracts"`
}

// Config is the loaded, validated project configuration.
type Config struct {
	Version     int         `yaml:"version"`
	Interactive Interactive `yaml:"interactive"`
	// Loaded is true when a file was found and validated.
	Loaded bool `yaml:"-"`
	// Path is where the file was read from ("" when absent).
	Path string `yaml:"-"`
}

// Path returns the configuration file path for a project root.
func Path(root string) string { return filepath.Join(root, Dir, File) }

// Load reads and validates the project configuration. A missing file returns
// a zero Config and no error. Unknown top-level keys are returned as warnings
// so newer shared configuration keeps working on older LazyAI versions.
func Load(root string) (Config, []string, error) {
	path := Path(root)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil, nil
	}
	if err != nil {
		return Config{}, nil, err
	}
	cfg, warnings, err := Parse(data)
	cfg.Path = path
	if err != nil {
		return cfg, warnings, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, warnings, nil
}

// Parse validates configuration bytes. On error the returned Config has
// strict mode off and no contracts.
func Parse(data []byte) (Config, []string, error) {
	var raw map[string]yaml.Node
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return Config{}, nil, fmt.Errorf("malformed yaml: %w", err)
	}
	known := map[string]bool{"version": true, "interactive": true}
	var warnings []string
	var keys []string
	for k := range raw {
		if !known[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		warnings = append(warnings, fmt.Sprintf("ignoring unknown key %q", k))
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, warnings, fmt.Errorf("malformed yaml: %w", err)
	}
	if err := validate(&cfg); err != nil {
		return Config{}, warnings, err
	}
	cfg.Loaded = true
	return cfg, warnings, nil
}

func validate(cfg *Config) error {
	if cfg.Version != Version {
		return fmt.Errorf("unsupported version %d (want %d)", cfg.Version, Version)
	}
	for name, c := range cfg.Interactive.Contracts {
		if strings.TrimSpace(name) == "" {
			return errors.New("contract name is empty")
		}
		if len(c.Fields) == 0 {
			return fmt.Errorf("contract %q has no fields", name)
		}
		seen := map[string]bool{}
		for i, f := range c.Fields {
			if strings.TrimSpace(f.Key) == "" {
				return fmt.Errorf("contract %q field %d has no key", name, i+1)
			}
			if strings.ContainsAny(f.Key, " \t\n:#") {
				return fmt.Errorf("contract %q field %q has an invalid key", name, f.Key)
			}
			if seen[f.Key] {
				return fmt.Errorf("contract %q has duplicate field key %q", name, f.Key)
			}
			seen[f.Key] = true
			if strings.TrimSpace(f.Label) == "" {
				return fmt.Errorf("contract %q field %q has no label", name, f.Key)
			}
			switch f.Type {
			case FieldText, FieldMultiline:
			case "":
				c.Fields[i].Type = FieldText
			default:
				return fmt.Errorf("contract %q field %q has unsupported type %q", name, f.Key, f.Type)
			}
		}
		c.Name = name
		cfg.Interactive.Contracts[name] = c
	}
	if cfg.Interactive.DefaultContract != "" {
		if _, ok := cfg.Interactive.Contracts[cfg.Interactive.DefaultContract]; !ok {
			return fmt.Errorf("default_contract %q is not defined", cfg.Interactive.DefaultContract)
		}
	}
	if cfg.Interactive.Strict && len(cfg.Interactive.Contracts) == 0 {
		return errors.New("strict mode requires at least one contract")
	}
	return nil
}

// Contract returns the template strict entry uses: the default contract, or
// the only one defined.
func (c Config) Contract() (Contract, bool) {
	if !c.Loaded || len(c.Interactive.Contracts) == 0 {
		return Contract{}, false
	}
	if c.Interactive.DefaultContract != "" {
		ct, ok := c.Interactive.Contracts[c.Interactive.DefaultContract]
		return ct, ok
	}
	if len(c.Interactive.Contracts) == 1 {
		for _, ct := range c.Interactive.Contracts {
			return ct, true
		}
	}
	names := make([]string, 0, len(c.Interactive.Contracts))
	for n := range c.Interactive.Contracts {
		names = append(names, n)
	}
	sort.Strings(names)
	return c.Interactive.Contracts[names[0]], true
}

// Missing lists required field keys, in template order, that have no
// non-blank value.
func (c Contract) Missing(values map[string]string) []string {
	var out []string
	for _, f := range c.Fields {
		if f.Required && strings.TrimSpace(values[f.Key]) == "" {
			out = append(out, f.Key)
		}
	}
	return out
}

// Render produces the deterministic YAML document sent to the agent: the
// contract name, then every non-blank field in template order as a literal
// block scalar. Control characters other than newline and tab are dropped so
// the text can never carry terminal control sequences into the child.
func (c Contract) Render(values map[string]string) string {
	var b strings.Builder
	b.WriteString("contract: ")
	b.WriteString(c.Name)
	b.WriteString("\n")
	for _, f := range c.Fields {
		v := sanitize(strings.TrimSpace(values[f.Key]))
		if v == "" {
			continue
		}
		b.WriteString(f.Key)
		b.WriteString(": |\n")
		for _, line := range strings.Split(v, "\n") {
			b.WriteString("  ")
			b.WriteString(strings.TrimRight(line, " \t"))
			b.WriteString("\n")
		}
	}
	return b.String()
}

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || r >= 0x20 && r != 0x7f {
			return r
		}
		return -1
	}, s)
}

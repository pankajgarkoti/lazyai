package config

import (
	"bytes"
	"errors"
	"os"

	"gopkg.in/yaml.v3"
)

// InitialChoices are the small set of project preferences offered at first launch.
type InitialChoices struct {
	Agent           Agent
	Strict          bool
	DefaultContract string
}

// NeedsSetup treats every existing entry, including a dangling symlink, as
// user-owned. Malformed existing files are reported by the normal config loader.
func NeedsSetup(root string) (bool, error) {
	_, err := os.Lstat(Path(root))
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	return false, err
}

// Defaults reads shipped templates without creating a project configuration.
func Defaults() (Config, error) {
	cfg, _, err := Parse(defaultYAML)
	return cfg, err
}

// Initialize publishes a complete commented configuration without overwriting
// an existing or concurrently-created file. No directories are created on an
// invalid choice. The returned bool distinguishes creation from a competing writer.
func Initialize(root string, choices InitialChoices) (bool, error) {
	needed, err := NeedsSetup(root)
	if err != nil || !needed {
		return false, err
	}
	cfg, err := Defaults()
	if err != nil {
		return false, err
	}
	cfg.Agent = choices.Agent
	cfg.Interactive.Strict = choices.Strict
	if choices.DefaultContract != "" {
		cfg.Interactive.DefaultContract = choices.DefaultContract
	}
	if err := validate(&cfg); err != nil {
		return false, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(defaultYAML, &doc); err != nil {
		return false, err
	}
	rootNode := doc.Content[0]
	agentNode := mappingValue(rootNode, "agent")
	if err := setMappingValue(agentNode, "backend", cfg.Agent.Backend); err != nil {
		return false, err
	}
	if cfg.Agent.Executable != "" {
		if err := setMappingValue(agentNode, "executable", cfg.Agent.Executable); err != nil {
			return false, err
		}
	}
	interactive := mappingValue(rootNode, "interactive")
	if err := setMappingValue(interactive, "strict", cfg.Interactive.Strict); err != nil {
		return false, err
	}
	if err := setMappingValue(interactive, "default_contract", cfg.Interactive.DefaultContract); err != nil {
		return false, err
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return false, err
	}
	if err := enc.Close(); err != nil {
		return false, err
	}
	return createFile(Path(root), out.Bytes())
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func setMappingValue(node *yaml.Node, key string, value any) error {
	var replacement yaml.Node
	if err := replacement.Encode(value); err != nil {
		return err
	}
	if old := mappingValue(node, key); old != nil {
		replacement.HeadComment, replacement.LineComment, replacement.FootComment = old.HeadComment, old.LineComment, old.FootComment
		*old = replacement
	} else {
		node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, &replacement)
	}
	return nil
}

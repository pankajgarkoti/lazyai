// Package integration materializes the bundled OpenCode plugin and skill into
// a config directory that is handed to OpenCode via OPENCODE_CONFIG_DIR.
//
// OpenCode treats OPENCODE_CONFIG_DIR as an *additional* config directory, so
// the user's global and project configuration keep working unchanged.
package integration

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

//go:embed assets v2
var assets embed.FS

// DefaultDir returns the stable per-user location for the materialized config
// directory. It is stable (not per-run) so OpenCode's dependency install for
// the plugin persists between launches.
func DefaultDir() (string, error) {
	return DefaultDirFor("opencode")
}

func DefaultDirFor(backend string) (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "lazyai", backend), nil
}

// Materialize writes the embedded assets under dir, only touching files whose
// contents differ. It returns the directory to use for OPENCODE_CONFIG_DIR.
func Materialize(dir string) (string, error) {
	return MaterializeFor(dir, "opencode")
}

func MaterializeFor(dir, backend string) (string, error) {
	source := "assets"
	if backend == "opencode2" {
		source = "v2"
	}
	err := fs.WalkDir(assets, source, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(source, p)
		target := filepath.Join(dir, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		want, err := assets.ReadFile(p)
		if err != nil {
			return err
		}
		have, err := os.ReadFile(target)
		if err == nil && bytes.Equal(have, want) {
			return nil
		}
		return os.WriteFile(target, want, 0o644)
	})
	if err != nil {
		return "", fmt.Errorf("materialize integration: %w", err)
	}
	return dir, nil
}

// V2 does not bundle @opencode/plugin for local TypeScript plugins. Install it
// once into the version-specific cache, not into the project or user's config.
func EnsureV2PluginAPI(dir string) error {
	if _, err := os.Stat(filepath.Join(dir, "node_modules", "@opencode", "plugin", "package.json")); err == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "npm", "install", "--prefix", dir, "--ignore-scripts", "--no-save", "--no-audit", "--no-fund", "@opencode/plugin@0.0.0-beta-19507")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("install OpenCode 2 plugin API: %w: %s", err, out)
	}
	return nil
}

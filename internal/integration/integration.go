// Package integration materializes the bundled OpenCode plugin and skill into
// a config directory that is handed to OpenCode via OPENCODE_CONFIG_DIR.
//
// OpenCode treats OPENCODE_CONFIG_DIR as an *additional* config directory, so
// the user's global and project configuration keep working unchanged.
package integration

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed assets
var assets embed.FS

// DefaultDir returns the stable per-user location for the materialized config
// directory. It is stable (not per-run) so OpenCode's dependency install for
// the plugin persists between launches.
func DefaultDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "lazyai", "opencode"), nil
}

// Materialize writes the embedded assets under dir, only touching files whose
// contents differ. It returns the directory to use for OPENCODE_CONFIG_DIR.
func Materialize(dir string) (string, error) {
	err := fs.WalkDir(assets, "assets", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel("assets", p)
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

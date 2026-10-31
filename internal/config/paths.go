// Package config locates, loads and resolves fv's configuration.
package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// Paths are the resolved locations fv reads and writes.
type Paths struct {
	// ConfigFile is the full path of config.toml.
	ConfigFile string
	// DataDir is the directory for fv's data files (fv.log, ...).
	DataDir string
}

// ResolvePaths is a pure function of the operating system name, an environment lookup, the
// user's home directory and the --config flag value (empty when not given). Precedence for
// the config file: flag, FV_CONFIG, platform default. The data dir: FV_DATA_DIR, platform
// default. Relative overrides are made absolute against the working directory.
func ResolvePaths(goos string, getenv func(string) string, home string, flagConfig string) (Paths, error) {
	var p Paths

	switch {
	case flagConfig != "":
		abs, err := absPath(goos, flagConfig)
		if err != nil {
			return Paths{}, fmt.Errorf("resolve --config %q: %w", flagConfig, err)
		}
		p.ConfigFile = abs
	case getenv("FV_CONFIG") != "":
		abs, err := absPath(goos, getenv("FV_CONFIG"))
		if err != nil {
			return Paths{}, fmt.Errorf("resolve FV_CONFIG %q: %w", getenv("FV_CONFIG"), err)
		}
		p.ConfigFile = abs
	default:
		dir, err := defaultConfigDir(goos, getenv, home)
		if err != nil {
			return Paths{}, err
		}
		p.ConfigFile = join(goos, dir, "config.toml")
	}

	if v := getenv("FV_DATA_DIR"); v != "" {
		abs, err := absPath(goos, v)
		if err != nil {
			return Paths{}, fmt.Errorf("resolve FV_DATA_DIR %q: %w", v, err)
		}
		p.DataDir = abs
	} else {
		dir, err := defaultDataDir(goos, getenv, home)
		if err != nil {
			return Paths{}, err
		}
		p.DataDir = dir
	}
	return p, nil
}

func defaultConfigDir(goos string, getenv func(string) string, home string) (string, error) {
	switch goos {
	case "windows":
		base := getenv("AppData")
		if base == "" {
			return "", errors.New("%AppData% is not set; set it or use --config / FV_CONFIG")
		}
		return join(goos, base, "fv"), nil
	case "darwin":
		return darwinDir(home)
	default:
		if x := getenv("XDG_CONFIG_HOME"); strings.HasPrefix(x, "/") {
			return join(goos, x, "fv"), nil
		}
		if home == "" {
			return "", errors.New("home directory unknown; set XDG_CONFIG_HOME or use --config / FV_CONFIG")
		}
		return join(goos, home, ".config", "fv"), nil
	}
}

func defaultDataDir(goos string, getenv func(string) string, home string) (string, error) {
	switch goos {
	case "windows":
		base := getenv("LocalAppData")
		if base == "" {
			return "", errors.New("%LocalAppData% is not set; set it or use FV_DATA_DIR")
		}
		return join(goos, base, "fv"), nil
	case "darwin":
		return darwinDir(home)
	default:
		if x := getenv("XDG_DATA_HOME"); strings.HasPrefix(x, "/") {
			return join(goos, x, "fv"), nil
		}
		if home == "" {
			return "", errors.New("home directory unknown; set XDG_DATA_HOME or use FV_DATA_DIR")
		}
		return join(goos, home, ".local", "share", "fv"), nil
	}
}

func darwinDir(home string) (string, error) {
	if home == "" {
		return "", errors.New("home directory unknown; use --config / FV_CONFIG and FV_DATA_DIR")
	}
	return join("darwin", home, "Library", "Application Support", "fv"), nil
}

// join joins with the separator of the target OS, so the result is right on any host.
func join(goos string, elems ...string) string {
	sep := "/"
	if goos == "windows" {
		sep = `\`
	}
	out := strings.TrimRight(elems[0], sep)
	for _, e := range elems[1:] {
		out += sep + e
	}
	return out
}

// absPath keeps a path that is already absolute for the target OS and otherwise resolves
// it against the working directory.
func absPath(goos, p string) (string, error) {
	if isAbs(goos, p) {
		return p, nil
	}
	return filepath.Abs(p)
}

func isAbs(goos, p string) bool {
	if goos == "windows" {
		return len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') || strings.HasPrefix(p, `\\`)
	}
	return strings.HasPrefix(p, "/")
}

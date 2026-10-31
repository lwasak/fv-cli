package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Load reads and strictly decodes the config file at path. Unknown keys and syntax errors
// are reported with the key or line. A missing file yields an error wrapping fs.ErrNotExist.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path is the user's own config location
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	// Windows PowerShell 5 and Notepad write a UTF-8 byte order mark; TOML does not allow one.
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	var cfg Config
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, describe(path, err)
	}
	return &cfg, nil
}

func describe(path string, err error) error {
	var strict *toml.StrictMissingError
	if errors.As(err, &strict) {
		var msgs []string
		for _, de := range strict.Errors {
			row, _ := de.Position()
			msgs = append(msgs, fmt.Sprintf("unknown key %q (line %d)", strings.Join(de.Key(), "."), row))
		}
		return fmt.Errorf("%s: %s", path, strings.Join(msgs, "; "))
	}
	var de *toml.DecodeError
	if errors.As(err, &de) {
		row, col := de.Position()
		key := ""
		if k := strings.Join(de.Key(), "."); k != "" {
			key = fmt.Sprintf(" at key %q", k)
		}
		return fmt.Errorf("%s: line %d, column %d%s: %s", path, row, col, key, de.Error())
	}
	return fmt.Errorf("%s: %w", path, err)
}

package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
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
		if hint, ok := quoteHint(de); ok {
			return fmt.Errorf("%s: line %d, column %d: %s", path, row, col, hint)
		}
		return fmt.Errorf("%s: line %d, column %d%s: %s", path, row, col, key, de.Error())
	}
	return fmt.Errorf("%s: %w", path, err)
}

// stringTypeRE matches go-toml's message for a TOML number, date or boolean written where
// the config expects a string.
var stringTypeRE = regexp.MustCompile(`cannot decode TOML \w+ into struct field \S+ of type string$`)

// quoteHint turns that type error into advice. Amounts and quantities are strings in the
// config (a float would lose the exact value), so a bare number is a common mistake.
func quoteHint(de *toml.DecodeError) (string, bool) {
	keys := de.Key()
	if len(keys) == 0 || !stringTypeRE.MatchString(de.Error()) {
		return "", false
	}
	full, last := strings.Join(keys, "."), keys[len(keys)-1]
	switch last {
	case "unit_price":
		return fmt.Sprintf("%s: write the amount as a quoted string, e.g. %s = \"120,50\"", full, last), true
	case "quantity":
		return fmt.Sprintf("%s: write the amount as a quoted string, e.g. %s = \"1,5\"", full, last), true
	}
	return fmt.Sprintf("%s: write the value as a quoted string, e.g. %s = \"…\"", full, last), true
}

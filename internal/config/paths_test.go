package config

import (
	"path/filepath"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestResolvePaths(t *testing.T) {
	abs := func(p string) string {
		a, err := filepath.Abs(p)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	tests := []struct {
		name       string
		goos       string
		env        map[string]string
		home       string
		flag       string
		wantConfig string
		wantData   string
		wantErr    bool
	}{
		{"windows default", "windows", map[string]string{"AppData": `C:\U\AppData\Roaming`, "LocalAppData": `C:\U\AppData\Local`}, `C:\U`, "",
			`C:\U\AppData\Roaming\fv\config.toml`, `C:\U\AppData\Local\fv`, false},
		{"darwin default", "darwin", nil, "/Users/a", "",
			"/Users/a/Library/Application Support/fv/config.toml", "/Users/a/Library/Application Support/fv", false},
		{"linux xdg set", "linux", map[string]string{"XDG_CONFIG_HOME": "/x/cfg", "XDG_DATA_HOME": "/x/data"}, "/home/a", "",
			"/x/cfg/fv/config.toml", "/x/data/fv", false},
		{"linux xdg unset", "linux", nil, "/home/a", "",
			"/home/a/.config/fv/config.toml", "/home/a/.local/share/fv", false},
		{"linux xdg relative ignored", "linux", map[string]string{"XDG_CONFIG_HOME": "rel/cfg", "XDG_DATA_HOME": "rel/data"}, "/home/a", "",
			"/home/a/.config/fv/config.toml", "/home/a/.local/share/fv", false},
		{"FV_CONFIG beats default", "linux", map[string]string{"FV_CONFIG": "/etc/my.toml"}, "/home/a", "",
			"/etc/my.toml", "/home/a/.local/share/fv", false},
		{"flag beats FV_CONFIG", "linux", map[string]string{"FV_CONFIG": "/etc/my.toml"}, "/home/a", "/tmp/flag.toml",
			"/tmp/flag.toml", "/home/a/.local/share/fv", false},
		{"FV_DATA_DIR linux", "linux", map[string]string{"FV_DATA_DIR": "/d"}, "/home/a", "",
			"/home/a/.config/fv/config.toml", "/d", false},
		{"FV_DATA_DIR darwin", "darwin", map[string]string{"FV_DATA_DIR": "/d"}, "/Users/a", "",
			"/Users/a/Library/Application Support/fv/config.toml", "/d", false},
		{"FV_DATA_DIR windows", "windows", map[string]string{"AppData": `C:\R`, "FV_DATA_DIR": `D:\data`}, `C:\U`, "",
			`C:\R\fv\config.toml`, `D:\data`, false},
		{"relative override made absolute", "linux", map[string]string{"FV_DATA_DIR": "rel/data"}, "/home/a", "rel/c.toml",
			abs("rel/c.toml"), abs("rel/data"), false},
		{"windows missing AppData", "windows", map[string]string{"LocalAppData": `C:\L`}, `C:\U`, "", "", "", true},
		{"windows missing LocalAppData", "windows", map[string]string{"AppData": `C:\R`}, `C:\U`, "", "", "", true},
		{"windows overrides need no AppData", "windows", map[string]string{"FV_CONFIG": `C:\c.toml`, "FV_DATA_DIR": `C:\d`}, "", "",
			`C:\c.toml`, `C:\d`, false},
		{"darwin missing home", "darwin", nil, "", "", "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolvePaths(tc.goos, env(tc.env), tc.home, tc.flag)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.ConfigFile != tc.wantConfig {
				t.Errorf("ConfigFile = %q, want %q", got.ConfigFile, tc.wantConfig)
			}
			if got.DataDir != tc.wantData {
				t.Errorf("DataDir = %q, want %q", got.DataDir, tc.wantData)
			}
		})
	}
}

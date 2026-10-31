package config

import (
	"strings"
	"testing"
)

func ptr(s string) *string { return &s }

func TestResolveEnv(t *testing.T) {
	tests := []struct {
		name    string
		flag    *string
		file    *string
		want    Env
		wantErr bool
	}{
		{"absent defaults to test", nil, nil, EnvTest, false},
		{"file demo", nil, ptr("demo"), EnvDemo, false},
		{"flag beats file", ptr("prod"), ptr("demo"), EnvProd, false},
		{"flag only", ptr("demo"), nil, EnvDemo, false},
		{"flag staging", ptr("staging"), nil, "", true},
		{"flag PROD case-sensitive", ptr("PROD"), nil, "", true},
		{"flag empty", ptr(""), nil, "", true},
		{"file staging", nil, ptr("staging"), "", true},
		{"file empty string", nil, ptr(""), "", true},
		{"bad file value even when flag valid", ptr("demo"), ptr("staging"), "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveEnv(tc.flag, tc.file)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got %q", got)
				}
				if !strings.Contains(err.Error(), "test, demo, prod") {
					t.Errorf("error does not list allowed values: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

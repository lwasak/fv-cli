package config

import "fmt"

// Env is the KSeF environment fv talks to.
type Env string

// The three KSeF environments.
const (
	EnvTest Env = "test"
	EnvDemo Env = "demo"
	EnvProd Env = "prod"
)

// ParseEnv accepts exactly "test", "demo" and "prod" (case-sensitive).
func ParseEnv(s string) (Env, error) {
	switch Env(s) {
	case EnvTest, EnvDemo, EnvProd:
		return Env(s), nil
	}
	return "", fmt.Errorf("invalid environment %q: allowed values are test, demo, prod", s)
}

// ResolveEnv picks the environment from the --env flag (nil when not given), else the
// ksef.environment file value (nil when absent), else test. A present value that is not
// allowed is an error even when another source would win.
func ResolveEnv(flag, file *string) (Env, error) {
	var fromFile Env
	if file != nil {
		e, err := ParseEnv(*file)
		if err != nil {
			return "", fmt.Errorf("ksef.environment: %w", err)
		}
		fromFile = e
	}
	if flag != nil {
		e, err := ParseEnv(*flag)
		if err != nil {
			return "", fmt.Errorf("--env: %w", err)
		}
		return e, nil
	}
	if file != nil {
		return fromFile, nil
	}
	return EnvTest, nil
}

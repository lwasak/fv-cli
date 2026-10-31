// Package useragent holds the User-Agent fv sends on every HTTP request. Some services
// (api.nbp.pl) refuse the default Go-http-client agent with 403.
package useragent

import "sync/atomic"

var version atomic.Value

func init() { version.Store("dev") }

// SetVersion records the fv version shown in the User-Agent. The cli package calls it with
// the build-time version when the root command is created.
func SetVersion(v string) {
	if v == "" {
		v = "dev"
	}
	version.Store(v)
}

// String returns "fv/<version>".
func String() string { return "fv/" + version.Load().(string) }

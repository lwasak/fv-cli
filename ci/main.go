// CI pipeline for fv: checks, snapshot builds and releases.
//
// Run locally with the Dagger CLI (engine v0.21.11):
//
//	dagger call check
//	dagger call build export --path=dist
//	dagger call release --tag=v0.1.0 --token=env:GITHUB_TOKEN
package main

import (
	"context"
	"fmt"
	"strings"

	"dagger/fv/internal/dagger"
)

const (
	goImage         = "golang:1.26.10-bookworm"
	goreleaserImage = "goreleaser/goreleaser:v2.19.0"
	govulncheckPkg  = "golang.org/x/vuln/cmd/govulncheck@v1.9.0"
	gosecPkg        = "github.com/securego/gosec/v2/cmd/gosec@v2.30.0"
)

// crossTargets are the platforms every release is built for.
var crossTargets = [][2]string{
	{"windows", "amd64"},
	{"linux", "amd64"},
	{"linux", "arm64"},
	{"darwin", "amd64"},
	{"darwin", "arm64"},
}

type Fv struct{}

// goBase returns a Go container with the source mounted and module/build caches.
func goBase(source *dagger.Directory) *dagger.Container {
	return dag.Container().
		From(goImage).
		WithMountedCache("/go/pkg/mod", dag.CacheVolume("fv-go-mod")).
		WithMountedCache("/root/.cache/go-build", dag.CacheVolume("fv-go-build")).
		WithEnvVariable("GOFLAGS", "-buildvcs=false").
		WithMountedDirectory("/src", source).
		WithWorkdir("/src")
}

// Check runs formatting, vet, tests, cross-builds, govulncheck and gosec.
func (m *Fv) Check(
	ctx context.Context,
	// +defaultPath="/"
	// +ignore=["dist", "ci", ".git"]
	source *dagger.Directory,
) (string, error) {
	// libxml2-utils provides xmllint, which the FA(3) XSD golden test needs.
	ctr := goBase(source).
		WithExec([]string{"apt-get", "update"}).
		WithExec([]string{"apt-get", "install", "-y", "--no-install-recommends", "libxml2-utils"})

	// gofmt -l prints the files that need formatting; any output fails the check.
	ctr = ctr.WithExec([]string{"sh", "-c",
		`out="$(gofmt -l .)"; if [ -n "$out" ]; then echo "gofmt needed for:"; echo "$out"; exit 1; fi`})

	ctr = ctr.
		WithExec([]string{"go", "vet", "./..."}).
		WithExec([]string{"go", "test", "-count=1", "./..."})

	// Cross-builds are pure compile checks; the binaries go to /tmp and are discarded.
	for _, t := range crossTargets {
		goos, goarch := t[0], t[1]
		ctr = ctr.
			WithEnvVariable("CGO_ENABLED", "0").
			WithEnvVariable("GOOS", goos).
			WithEnvVariable("GOARCH", goarch).
			WithExec([]string{"go", "build", "-trimpath", "-o", "/tmp/fv-" + goos + "-" + goarch, "./cmd/fv"})
	}
	// Back to the host platform for the tools below.
	ctr = ctr.
		WithoutEnvVariable("GOOS").
		WithoutEnvVariable("GOARCH")

	ctr = ctr.
		WithExec([]string{"go", "run", govulncheckPkg, "./..."}).
		WithExec([]string{"go", "run", gosecPkg, "-quiet", "./..."})

	out, err := ctr.Stdout(ctx)
	if err != nil {
		return "", fmt.Errorf("check failed: %w", err)
	}
	return strings.TrimSpace(out) + "\ncheck: ok\n", nil
}

// goreleaser returns a GoReleaser container with the source mounted.
func goreleaser(source *dagger.Directory) *dagger.Container {
	return dag.Container().
		From(goreleaserImage).
		WithMountedCache("/go/pkg/mod", dag.CacheVolume("fv-go-mod")).
		WithMountedCache("/root/.cache/go-build", dag.CacheVolume("fv-go-build")).
		WithEnvVariable("GOFLAGS", "-buildvcs=false").
		WithDirectory("/src", source).
		WithWorkdir("/src")
}

// ensureGit makes /src a git repository with one commit when it has no .git
// (Dagger may not upload it), and tags it, so GoReleaser can work.
func ensureGit(ctr *dagger.Container, tag string) *dagger.Container {
	script := `set -eu
if [ ! -d .git ]; then
  git init -q
  git config user.email "ci@localhost"
  git config user.name "ci"
  git add -A
  git commit -q -m "release snapshot"
fi
git tag -f "$GORELEASER_CURRENT_TAG" >/dev/null
`
	return ctr.
		WithEnvVariable("GORELEASER_CURRENT_TAG", tag).
		WithExec([]string{"sh", "-c", script})
}

// Build creates snapshot archives (no publishing) and returns the dist/ directory.
func (m *Fv) Build(
	ctx context.Context,
	// +defaultPath="/"
	// +ignore=["dist"]
	source *dagger.Directory,
	// +default="snapshot"
	version string,
) *dagger.Directory {
	tag := version
	if !strings.HasPrefix(tag, "v") {
		tag = "v0.0.0-" + tag
	}
	ctr := ensureGit(goreleaser(source), tag).
		WithExec([]string{"goreleaser", "release", "--snapshot", "--clean"})
	return ctr.Directory("/src/dist")
}

// Release builds the archives and publishes them to the GitHub release for tag.
func (m *Fv) Release(
	ctx context.Context,
	// +defaultPath="/"
	// +ignore=["dist"]
	source *dagger.Directory,
	// Git tag to release, for example v0.1.0.
	tag string,
	// GitHub token with contents:write on the repository.
	token *dagger.Secret,
) (string, error) {
	ctr := ensureGit(goreleaser(source), tag).
		WithSecretVariable("GITHUB_TOKEN", token).
		WithExec([]string{"goreleaser", "release", "--clean"})
	return ctr.Stdout(ctx)
}

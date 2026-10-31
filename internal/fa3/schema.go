package fa3

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/lwasak/fv-cli/internal/fsafe"
)

//go:embed schema
var schemaEmbed embed.FS

// SchemaFile is the FA(3) entry schema inside SchemaFS.
const SchemaFile = "schemat_FA(3)_v1-0E.xsd"

// ErrNoXmllint is returned by ValidateWithXmllint when xmllint is not on PATH.
var ErrNoXmllint = errors.New("xmllint not found on PATH")

// SchemaFS returns the embedded schema directory: SchemaFile plus bazowe/.
func SchemaFS() fs.FS {
	sub, err := fs.Sub(schemaEmbed, "schema")
	if err != nil {
		panic(err) // the embedded directory always exists
	}
	return sub
}

// ValidateWithXmllint validates xml against the vendored schema with
// `xmllint --noout --nonet --schema`. The schema is extracted into workDir (a
// temporary directory when workDir is empty and removed afterwards).
func ValidateWithXmllint(ctx context.Context, xml []byte, workDir string) error {
	bin, err := exec.LookPath("xmllint")
	if err != nil {
		return ErrNoXmllint
	}
	if workDir == "" {
		workDir, err = os.MkdirTemp("", "fv-xsd-")
		if err != nil {
			return fmt.Errorf("fa3: temp dir: %w", err)
		}
		defer os.RemoveAll(workDir)
	}
	xsd, err := extractSchema(workDir)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, bin, "--noout", "--nonet", "--schema", xsd, "-") // #nosec G204 -- fixed xmllint arguments; xsd is a path fv extracted itself
	cmd.Stdin = bytes.NewReader(xml)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("invoice does not validate against FA(3): %s", strings.TrimSpace(out.String()))
	}
	return nil
}

func extractSchema(dir string) (string, error) {
	fsys := SchemaFS()
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dir, filepath.FromSlash(p))
		if d.IsDir() {
			return fsafe.MkdirAll(target)
		}
		if path.Ext(p) != ".xsd" {
			return nil
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		return fsafe.WriteFile(target, b)
	})
	if err != nil {
		return "", fmt.Errorf("fa3: extract schema: %w", err)
	}
	return filepath.Join(dir, SchemaFile), nil
}

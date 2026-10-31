package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/lwasak/fv-cli/internal/exitcode"
)

func runWithErr(t *testing.T, err error) (int, string) {
	t.Helper()
	var stderr bytes.Buffer
	deps := Deps{Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: &stderr}
	root := &cobra.Command{Use: "fv", SilenceUsage: true, SilenceErrors: true,
		RunE: func(*cobra.Command, []string) error { return err }}
	return execute(deps, root, nil), stderr.String()
}

func TestExecute_ExitCodeSentinels(t *testing.T) {
	cause := errors.New("white list down")
	tests := []struct {
		err  error
		want int
	}{
		{errors.New("plain"), 1},
		{exitcode.Wrap(exitcode.ErrRegistry, cause), 2},
		{fmt.Errorf("x: %w", exitcode.ErrRejected), 4},
		{exitcode.ErrUnknown, 5},
		{exitcode.ErrAborted, 10},
		{errCredential, 3},
	}
	for _, tt := range tests {
		if got, _ := runWithErr(t, tt.err); got != tt.want {
			t.Errorf("%v: exit %d, want %d", tt.err, got, tt.want)
		}
	}
}

func TestExecute_AbortedMessage(t *testing.T) {
	code, out := runWithErr(t, exitcode.ErrAborted)
	if code != 10 || out != "aborted, nothing was sent\n" {
		t.Errorf("code=%d out=%q", code, out)
	}
	_, out = runWithErr(t, exitcode.Wrap(exitcode.ErrRegistry, errors.New("boom")))
	if out != "fv: boom\n" {
		t.Errorf("registry out = %q", out)
	}
}

func TestSession_Seams(t *testing.T) {
	s := &session{deps: Deps{Stdin: strings.NewReader("")}}
	base, hc := s.endpoint("nbp", "https://api.nbp.pl")
	if base != "https://api.nbp.pl" || hc == nil {
		t.Errorf("default endpoint = %q,%v", base, hc)
	}
	if s.confirmer() != nil {
		t.Error("confirmer must be nil without a terminal")
	}
	if s.rand() == nil {
		t.Error("rand must default to crypto/rand")
	}
	s.deps.Endpoints = func(svc string) (string, *http.Client) { return "https://x/" + svc, nil }
	if base, hc := s.endpoint("vies", "d"); base != "https://x/vies" || hc == nil {
		t.Errorf("seam endpoint = %q,%v", base, hc)
	}
	s.deps.Confirm = func(string) (string, error) { return "PRODUCTION", nil }
	if got, _ := s.confirmer()("?"); got != "PRODUCTION" {
		t.Errorf("confirm = %q", got)
	}
	r := strings.NewReader("z")
	s.deps.Rand = r
	if s.rand() != io.Reader(r) {
		t.Error("Deps.Rand not used")
	}
}

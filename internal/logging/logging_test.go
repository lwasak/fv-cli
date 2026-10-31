package logging

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRedactor_ReplacesRegistered(t *testing.T) {
	r := &Redactor{}
	r.Register("s3cr3t-value")
	r.Register("other")
	got := r.Redact("a s3cr3t-value b other c s3cr3t-value")
	if got != "a [REDACTED] b [REDACTED] c [REDACTED]" {
		t.Errorf("got %q", got)
	}
}

func TestRedactor_IgnoresEmpty(t *testing.T) {
	r := &Redactor{}
	r.Register("")
	if got := r.Redact("abc"); got != "abc" {
		t.Errorf("got %q", got)
	}
}

func writeN(t *testing.T, w *rotator, s string) {
	t.Helper()
	if _, err := w.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
}

func TestRotate_RotatesAtLimit(t *testing.T) {
	dir := t.TempDir()
	w := newRotator(filepath.Join(dir, "fv.log"), 10)
	defer w.Close()
	writeN(t, w, "123456\n")
	writeN(t, w, "abcdef\n")
	rotated, err := os.ReadFile(filepath.Join(dir, "fv.log.1"))
	if err != nil || string(rotated) != "123456\n" {
		t.Fatalf("fv.log.1 = %q, err %v", rotated, err)
	}
	cur, _ := os.ReadFile(filepath.Join(dir, "fv.log"))
	if string(cur) != "abcdef\n" {
		t.Errorf("fv.log = %q", cur)
	}
}

func TestRotate_KeepsThreeFiles(t *testing.T) {
	dir := t.TempDir()
	w := newRotator(filepath.Join(dir, "fv.log"), 5)
	defer w.Close()
	for _, s := range []string{"aaaa\n", "bbbb\n", "cccc\n", "dddd\n", "eeee\n"} {
		writeN(t, w, s)
	}
	for i, want := range map[string]string{"fv.log": "eeee\n", "fv.log.1": "dddd\n", "fv.log.2": "cccc\n", "fv.log.3": "bbbb\n"} {
		got, err := os.ReadFile(filepath.Join(dir, i))
		if err != nil || string(got) != want {
			t.Errorf("%s = %q, err %v, want %q", i, got, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "fv.log.4")); err == nil {
		t.Error("fv.log.4 must not exist")
	}
}

func TestLogger_LazyCreateNoFileUntilWrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	l, closeFn := New(dir)
	defer closeFn()
	if _, err := os.Stat(dir); err == nil {
		t.Fatal("data dir created before first write")
	}
	l.Info("hello", "k", "v")
	data, err := os.ReadFile(filepath.Join(dir, "fv.log"))
	if err != nil || !strings.Contains(string(data), "hello") {
		t.Fatalf("log = %q, err %v", data, err)
	}
}

func TestLogger_RedactsSecretWithQuoteBackslashSpace(t *testing.T) {
	secret := `pa ss"wo\rd`
	Default.Register(secret)
	dir := t.TempDir()
	l, closeFn := New(dir)
	defer closeFn()
	l.Info("msg "+secret+" end", "k", secret, "err", fmt.Errorf("bad %s", secret), slog.Group("g", "n", secret))
	l.With("w", secret).WithGroup("grp").Info("second", "k", secret)
	data, _ := os.ReadFile(filepath.Join(dir, "fv.log"))
	log := string(data)
	for _, form := range []string{secret, `pa ss\"wo\\rd`, `wo\rd`, `ss"wo`} {
		if strings.Contains(log, form) {
			t.Errorf("log contains %q: %q", form, log)
		}
	}
	if !strings.Contains(log, "[REDACTED]") {
		t.Errorf("no placeholder: %q", log)
	}
	// stderr path: printed errors go through Default.Redact on the raw text.
	if got := Default.Redact("fv: bad " + secret); strings.Contains(got, secret) {
		t.Errorf("stderr text = %q", got)
	}
}

func TestLogger_RedactsDefaultRegistered(t *testing.T) {
	Default.Register("zzz-secret")
	dir := t.TempDir()
	l, closeFn := New(dir)
	defer closeFn()
	l.Info("value zzz-secret here")
	data, _ := os.ReadFile(filepath.Join(dir, "fv.log"))
	if strings.Contains(string(data), "zzz-secret") || !strings.Contains(string(data), "[REDACTED]") {
		t.Errorf("log = %q", data)
	}
}

func TestRedact_LongestFirst(t *testing.T) {
	for _, order := range [][]string{{"abc", "abcdef"}, {"abcdef", "abc"}} {
		r := &Redactor{}
		for _, s := range order {
			r.Register(s)
		}
		if got := r.Redact("abcdef"); got != "[REDACTED]" {
			t.Errorf("order %v: got %q", order, got)
		}
	}
}

func TestRedactor_ResetForgetsSecrets(t *testing.T) {
	r := &Redactor{}
	r.Register("abc123")
	r.Reset()
	if got := r.Redact("abc123"); got != "abc123" {
		t.Errorf("got %q", got)
	}
}

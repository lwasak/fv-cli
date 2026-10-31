package secrets

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/logging"

	"github.com/zalando/go-keyring"
)

type fakeBackend struct {
	data map[string]string
	err  error
	got  []string
}

func (f *fakeBackend) Get(service, user string) (string, error) {
	f.got = append(f.got, service+"|"+user)
	if f.err != nil {
		return "", f.err
	}
	v, ok := f.data[service]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (f *fakeBackend) Set(service, user, value string) error {
	if f.err != nil {
		return f.err
	}
	if f.data == nil {
		f.data = map[string]string{}
	}
	f.data[service] = value
	f.got = append(f.got, "set:"+service+"|"+user)
	return nil
}

const nip = "5260250274"

func TestSet_WritesKeyringKey(t *testing.T) {
	b := &fakeBackend{}
	s := newStore(b, nil)
	if err := s.Set("ksef", config.EnvDemo, nip, "pass-1"); err != nil {
		t.Fatal(err)
	}
	if b.data["fv/ksef/demo/"+nip] != "pass-1" {
		t.Fatalf("data = %v", b.data)
	}
	if b.got[0] != "set:fv/ksef/demo/"+nip+"|fv" {
		t.Fatalf("backend call = %q", b.got[0])
	}
	if got := s.Redact.Redact("a pass-1 b"); got != "a [REDACTED] b" {
		t.Fatalf("value not registered with redactor: %q", got)
	}
	v, err := s.Get("ksef", config.EnvDemo, nip)
	if err != nil || v != "pass-1" {
		t.Fatalf("get after set = %q, %v", v, err)
	}
}

func TestSet_RejectsInvalidPurposeOrNIP(t *testing.T) {
	b := &fakeBackend{}
	s := newStore(b, nil)
	for _, c := range []struct{ purpose, nip string }{
		{"", nip}, {"Ksef", nip}, {"a/b", nip}, {"ksef", "123"}, {"ksef", ""},
	} {
		if err := s.Set(c.purpose, config.EnvTest, c.nip, "v"); err == nil {
			t.Errorf("Set(%q,%q) accepted", c.purpose, c.nip)
		}
	}
	if err := s.Set("ksef", config.Env("bad"), nip, "v"); err == nil {
		t.Error("invalid env accepted")
	}
	if err := s.Set("ksef", config.EnvTest, nip, ""); err == nil {
		t.Error("empty value accepted")
	}
	if len(b.data) != 0 {
		t.Fatalf("backend written: %v", b.data)
	}
}

func newStore(b Backend, env map[string]string) *Store {
	return &Store{
		Backend: b,
		Getenv:  func(k string) string { return env[k] },
		Redact:  &logging.Redactor{},
	}
}

func TestGet_KeyringHit(t *testing.T) {
	b := &fakeBackend{data: map[string]string{"fv/ksef-token/test/" + nip: "tok-1"}}
	s := newStore(b, nil)
	v, err := s.Get("ksef-token", config.EnvTest, nip)
	if err != nil || v != "tok-1" {
		t.Fatalf("got %q, %v", v, err)
	}
	if b.got[0] != "fv/ksef-token/test/"+nip+"|fv" {
		t.Fatalf("backend call = %q", b.got[0])
	}
}

func TestGet_EnvFallback(t *testing.T) {
	var buf bytes.Buffer
	s := newStore(&fakeBackend{}, map[string]string{"FV_SECRET_KSEF_TOKEN_DEMO_" + nip: "from-env"})
	s.Log = slog.New(slog.NewTextHandler(&buf, nil))
	v, err := s.Get("ksef-token", config.EnvDemo, nip)
	if err != nil || v != "from-env" {
		t.Fatalf("got %q, %v", v, err)
	}
	if !strings.Contains(buf.String(), "level=WARN") || strings.Contains(buf.String(), "from-env") {
		t.Fatalf("warn log wrong: %q", buf.String())
	}
}

func TestGet_KeyringErrorIsReturned(t *testing.T) {
	boom := errors.New("keyring locked")
	s := newStore(&fakeBackend{err: boom}, map[string]string{"FV_SECRET_KSEF_TOKEN_TEST_" + nip: "x"})
	_, err := s.Get("ksef-token", config.EnvTest, nip)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want keyring error", err)
	}
}

func TestGet_MissNamesKeyNotValue(t *testing.T) {
	s := newStore(&fakeBackend{}, nil)
	_, err := s.Get("ksef-token", config.EnvProd, nip)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "fv/ksef-token/prod/"+nip) {
		t.Fatalf("error does not name key: %v", err)
	}
}

func TestGet_KeysDifferPerEnvironment(t *testing.T) {
	b := &fakeBackend{data: map[string]string{
		"fv/ksef-token/test/" + nip: "t",
		"fv/ksef-token/prod/" + nip: "p",
	}}
	s := newStore(b, nil)
	vt, _ := s.Get("ksef-token", config.EnvTest, nip)
	vp, _ := s.Get("ksef-token", config.EnvProd, nip)
	if vt != "t" || vp != "p" {
		t.Fatalf("got %q %q", vt, vp)
	}
	if _, err := s.Get("ksef-token", config.EnvDemo, nip); !errors.Is(err, ErrNotFound) {
		t.Fatalf("demo err = %v", err)
	}
}

func TestGet_RegistersValueWithRedactor(t *testing.T) {
	s := newStore(&fakeBackend{data: map[string]string{"fv/ksef-token/test/" + nip: "s3cr3t-value"}}, nil)
	if _, err := s.Get("ksef-token", config.EnvTest, nip); err != nil {
		t.Fatal(err)
	}
	if got := s.Redact.Redact("x s3cr3t-value y"); got != "x [REDACTED] y" {
		t.Fatalf("redact = %q", got)
	}

	s2 := newStore(&fakeBackend{}, map[string]string{"FV_SECRET_KSEF_TOKEN_TEST_" + nip: "env-secret"})
	if _, err := s2.Get("ksef-token", config.EnvTest, nip); err != nil {
		t.Fatal(err)
	}
	if got := s2.Redact.Redact("env-secret"); got != "[REDACTED]" {
		t.Fatalf("env redact = %q", got)
	}
}

func TestGet_RejectsInvalidPurposeOrNIP(t *testing.T) {
	s := newStore(&fakeBackend{}, nil)
	for _, c := range []struct{ purpose, nip string }{
		{"", nip}, {"Ksef", nip}, {"a/b", nip}, {"a_b", nip},
		{"ksef-token", "123"}, {"ksef-token", "52602502ab"}, {"ksef-token", ""},
	} {
		_, err := s.Get(c.purpose, config.EnvTest, c.nip)
		if err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("Get(%q,%q) err = %v, want validation error", c.purpose, c.nip, err)
		}
	}
	if _, err := s.Get("ksef-token", config.Env("bad"), nip); err == nil {
		t.Error("invalid env accepted")
	}
}

func TestGet_UnavailableFallsBackToEnv(t *testing.T) {
	s := newStore(&fakeBackend{err: fmt.Errorf("%w: no dbus", ErrUnavailable)},
		map[string]string{"FV_SECRET_KSEF_TOKEN_TEST_" + nip: "from-env"})
	v, err := s.Get("ksef-token", config.EnvTest, nip)
	if err != nil || v != "from-env" {
		t.Fatalf("got %q, %v", v, err)
	}
}

func TestGet_UnavailableNoEnvIsError(t *testing.T) {
	s := newStore(&fakeBackend{err: ErrUnavailable}, nil)
	_, err := s.Get("ksef-token", config.EnvTest, nip)
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "FV_SECRET_KSEF_TOKEN_TEST_"+nip) {
		t.Fatalf("message does not name the variable: %v", err)
	}
}

func TestGet_FallbackLogHasNoValue(t *testing.T) {
	var buf bytes.Buffer
	s := newStore(&fakeBackend{err: ErrUnavailable},
		map[string]string{"FV_SECRET_KSEF_TOKEN_TEST_" + nip: "sup3r-secret"})
	s.Log = slog.New(slog.NewTextHandler(&buf, nil))
	if _, err := s.Get("ksef-token", config.EnvTest, nip); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"level=WARN", "secret read from environment variable", `reason="keyring unavailable"`, "FV_SECRET_KSEF_TOKEN_TEST_" + nip, "fv/ksef-token/test/" + nip} {
		if !strings.Contains(out, want) {
			t.Errorf("log missing %q: %s", want, out)
		}
	}
	if strings.Contains(out, "sup3r-secret") {
		t.Fatalf("log leaks value: %s", out)
	}
}

func TestKeyringBackend_ClassifiesDBusError(t *testing.T) {
	for _, c := range []struct {
		err  error
		want bool
	}{
		{keyring.ErrUnsupportedPlatform, true},
		{errors.New("The name org.freedesktop.secrets was not provided"), true},
		{errors.New("exec: \"dbus-launch\": not found"), true},
		{errors.New("Failed to connect to DBUS"), true},
		{errors.New("keyring locked"), false},
	} {
		got := classify(c.err)
		if errors.Is(got, ErrUnavailable) != c.want {
			t.Errorf("classify(%v) = %v, unavailable=%v want %v", c.err, got, !c.want, c.want)
		}
		if !c.want && got != c.err {
			t.Errorf("classify changed unrelated error: %v", got)
		}
	}
	if classify(nil) != nil {
		t.Error("classify(nil) != nil")
	}
}

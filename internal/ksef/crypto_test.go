package ksef

import (
	"bytes"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"strings"
	"testing"

	"github.com/lwasak/fv-cli/internal/credentials/credtest"
	"github.com/lwasak/fv-cli/internal/logging"
)

func seqReader(n int, start byte) io.Reader {
	b := make([]byte, n)
	for i := range b {
		b[i] = start + byte(i)
	}
	return bytes.NewReader(b)
}

func TestSessionKey_KeyThenIVFromInjectedReader(t *testing.T) {
	k, err := newSessionKey(seqReader(48, 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(k.key) != 32 || len(k.iv) != 16 {
		t.Fatalf("key %d bytes, iv %d bytes", len(k.key), len(k.iv))
	}
	if k.key[0] != 1 || k.key[31] != 32 || k.iv[0] != 33 || k.iv[15] != 48 {
		t.Errorf("key/iv not read in order: %x %x", k.key, k.iv)
	}
}

func TestSessionKey_ShortReaderFails(t *testing.T) {
	if _, err := newSessionKey(seqReader(47, 1)); err == nil {
		t.Fatal("a reader with 47 bytes gave a key")
	}
}

func TestSessionKey_RegisteredWithRedactorInEveryForm(t *testing.T) {
	k, err := newSessionKey(seqReader(48, 100))
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range [][]byte{k.key, k.iv} {
		for name, form := range map[string]string{
			"base64":    base64.StdEncoding.EncodeToString(b),
			"hex":       hex.EncodeToString(b),
			"HEX upper": strings.ToUpper(hex.EncodeToString(b)),
		} {
			if got := logging.Default.Redact("x " + form + " y"); strings.Contains(got, form) {
				t.Errorf("%s form of key material not redacted: %q", name, got)
			}
		}
	}
}

func TestEncrypt_PKCS7AlwaysPadsAndRoundTrips(t *testing.T) {
	k, err := newSessionKey(seqReader(48, 7))
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{0, 1, 15, 16, 17, 32, 1000} {
		plain := bytes.Repeat([]byte("a"), n)
		ct, err := k.encrypt(plain)
		if err != nil {
			t.Fatal(err)
		}
		want := (n/16 + 1) * 16
		if len(ct) != want {
			t.Errorf("len(plain)=%d: ciphertext %d bytes, want %d", n, len(ct), want)
		}
		back, err := k.decrypt(ct)
		if err != nil || !bytes.Equal(back, plain) {
			t.Errorf("len(plain)=%d: round trip = %q, %v", n, back, err)
		}
	}
}

func TestEncrypt_KnownAnswerFromOpenSSL(t *testing.T) {
	// printf hello | openssl enc -aes-256-cbc -K 00..1f -iv 20..2f | base64
	k := &sessionKey{key: seqBytes(32, 0), iv: seqBytes(16, 32)}
	ct, err := k.encrypt([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := base64.StdEncoding.EncodeToString(ct), "z1xs5KePueqzD6d3dJV0kg=="; got != want {
		t.Errorf("ciphertext = %s, want %s", got, want)
	}
}

func seqBytes(n int, start byte) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = start + byte(i)
	}
	return b
}

func TestWrap_RSAOAEPSHA256RoundTrip(t *testing.T) {
	c := credtest.New(t, credtest.Spec{RSA: true})
	k, err := newSessionKey(seqReader(48, 9))
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := k.wrap(c.Cert)
	if err != nil {
		t.Fatal(err)
	}
	priv := c.Key.(*rsa.PrivateKey)
	got, err := rsa.DecryptOAEP(sha256.New(), nil, priv, wrapped, nil)
	if err != nil || !bytes.Equal(got, k.key) {
		t.Fatalf("OAEP-SHA256 unwrap = %x, %v", got, err)
	}
	if _, err := rsa.DecryptOAEP(sha256.New(), nil, priv, wrapped, []byte("label")); err == nil {
		t.Error("the key was wrapped with a label")
	}
}

func TestWrap_RefusesNonRSACertificate(t *testing.T) {
	c := credtest.New(t, credtest.Spec{}) // P-256
	k, _ := newSessionKey(seqReader(48, 1))
	_, err := k.wrap(c.Cert)
	if err == nil || !strings.Contains(err.Error(), "RSA") {
		t.Fatalf("err = %v, want an RSA refusal", err)
	}
}

func TestSessionKey_ZeroWipes(t *testing.T) {
	k, _ := newSessionKey(seqReader(48, 1))
	k.zero()
	if !bytes.Equal(k.key, make([]byte, 32)) || !bytes.Equal(k.iv, make([]byte, 16)) {
		t.Error("key material not wiped")
	}
	if k.live() {
		t.Error("wiped key reports live")
	}
}

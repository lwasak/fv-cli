package ksef

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/lwasak/fv-cli/internal/logging"
)

const (
	aesKeySize = 32 // AES-256
	ivSize     = aes.BlockSize
)

// sessionKey is the symmetric key material of one online session.
type sessionKey struct {
	key, iv []byte
	wiped   bool
}

// newSessionKey reads a 256-bit key and a 128-bit IV from rnd and registers every textual
// form of them (base64, hex in both cases) with the log redactor before anything else can
// happen to them.
func newSessionKey(rnd io.Reader) (*sessionKey, error) {
	if rnd == nil {
		rnd = rand.Reader
	}
	k := &sessionKey{key: make([]byte, aesKeySize), iv: make([]byte, ivSize)}
	if _, err := io.ReadFull(rnd, k.key); err != nil {
		return nil, fmt.Errorf("ksef: generate session key: %w", err)
	}
	if _, err := io.ReadFull(rnd, k.iv); err != nil {
		k.zero()
		return nil, fmt.Errorf("ksef: generate session IV: %w", err)
	}
	for _, b := range [][]byte{k.key, k.iv} {
		logging.Default.Register(base64.StdEncoding.EncodeToString(b))
		h := hex.EncodeToString(b)
		logging.Default.Register(h)
		logging.Default.Register(strings.ToUpper(h))
	}
	return k, nil
}

// zero wipes the key material.
func (k *sessionKey) zero() {
	clear(k.key)
	clear(k.iv)
	k.wiped = true
}

func (k *sessionKey) live() bool { return !k.wiped }

// encrypt returns plain as AES-256-CBC with PKCS#7 padding (always at least one pad byte; a
// whole block when the input is block-aligned).
func (k *sessionKey) encrypt(plain []byte) ([]byte, error) {
	blk, err := aes.NewCipher(k.key)
	if err != nil {
		return nil, fmt.Errorf("ksef: AES: %w", err)
	}
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	buf := make([]byte, len(plain)+pad)
	copy(buf, plain)
	copy(buf[len(plain):], bytes.Repeat([]byte{byte(pad)}, pad))
	out := make([]byte, len(buf))
	cipher.NewCBCEncrypter(blk, k.iv).CryptBlocks(out, buf)
	clear(buf)
	return out, nil
}

// wrap encrypts the AES key with the Ministry's RSA public key: RSA-OAEP with SHA-256 and
// MGF1-SHA-256 (Go uses the same hash for both). A non-RSA certificate is refused.
func (k *sessionKey) wrap(cert *x509.Certificate) ([]byte, error) {
	pub, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("ksef: the SymmetricKeyEncryption certificate holds a %T key; RSA is required", cert.PublicKey)
	}
	// OAEP's own randomness always comes from crypto/rand; only the session key and IV come
	// from the injected reader.
	out, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, pub, k.key, nil)
	if err != nil {
		return nil, fmt.Errorf("ksef: encrypt the session key: %w", err)
	}
	return out, nil
}

// errNotPadded is returned by unpad.
var errNotPadded = errors.New("ksef: invalid PKCS#7 padding")

// decrypt is the inverse of encrypt; only tests need it.
func (k *sessionKey) decrypt(ct []byte) ([]byte, error) {
	if len(ct) == 0 || len(ct)%aes.BlockSize != 0 {
		return nil, errNotPadded
	}
	blk, err := aes.NewCipher(k.key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(ct))
	cipher.NewCBCDecrypter(blk, k.iv).CryptBlocks(out, ct)
	pad := int(out[len(out)-1])
	if pad == 0 || pad > aes.BlockSize || pad > len(out) {
		return nil, errNotPadded
	}
	for _, b := range out[len(out)-pad:] {
		if int(b) != pad {
			return nil, errNotPadded
		}
	}
	return out[:len(out)-pad], nil
}

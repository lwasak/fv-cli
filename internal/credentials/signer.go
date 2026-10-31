package credentials

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"math/big"
	"os"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/fsafe"
)

// Credential is an imported login certificate. The private key stays encrypted on disk and
// is decrypted only inside Sign.
type Credential struct {
	// Source finds the key passphrase at signing time.
	Source PassphraseSource

	env     config.Env
	nip     string
	cert    *x509.Certificate
	keyFile string
	isP12   bool
}

// Certificate returns the login certificate.
func (c *Credential) Certificate() *x509.Certificate { return c.cert }

// Info inspects the certificate (subject identifier, type, serial, validity).
func (c *Credential) Info() (Info, error) { return Inspect(c.cert) }

// KeyFile is the path of the stored encrypted key.
func (c *Credential) KeyFile() string { return c.keyFile }

// Sign signs digest (already hashed with hash). RSA keys give a PKCS#1 v1.5 signature; ECDSA
// keys give the raw r||s form XML-DSig uses. The key file must be private to the user. The
// key is decrypted for this call only and dropped afterwards.
func (c *Credential) Sign(digest []byte, hash crypto.Hash) ([]byte, error) {
	if err := fsafe.CheckPrivate(c.keyFile); err != nil {
		return nil, fmt.Errorf("refusing to use the KSeF key: %w", err)
	}
	pass, err := c.Source.Get(c.env, c.nip)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(c.keyFile)
	if err != nil {
		return nil, fmt.Errorf("read KSeF key: %w", err)
	}
	defer zero(data)

	var key crypto.Signer
	if c.isP12 {
		key, _, err = decryptP12(data, pass)
	} else {
		b, perr := classifyKeyBlock(data)
		if perr != nil {
			return nil, perr
		}
		key, err = decryptPEMKey(b, pass)
		zero(b.Bytes)
	}
	if err != nil {
		return nil, err
	}

	switch k := key.(type) {
	case *rsa.PrivateKey:
		return k.Sign(rand.Reader, digest, hash)
	case *ecdsa.PrivateKey:
		if hash != crypto.SHA256 || k.Curve.Params().BitSize != 256 {
			return nil, errors.New("unsupported ECDSA key: only P-256 with SHA-256 is supported")
		}
		der, err := k.Sign(rand.Reader, digest, hash)
		if err != nil {
			return nil, err
		}
		var rs struct{ R, S *big.Int }
		if _, err := asn1.Unmarshal(der, &rs); err != nil {
			return nil, err
		}
		out := make([]byte, 64)
		rs.R.FillBytes(out[:32])
		rs.S.FillBytes(out[32:])
		return out, nil
	}
	return nil, errors.New("unsupported key type")
}

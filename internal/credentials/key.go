package credentials

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"

	"github.com/youmark/pkcs8"
	pkcs12 "software.sslmate.com/src/go-pkcs12"
)

const pemEncryptedKey = "ENCRYPTED PRIVATE KEY"

// classifyKeyBlock finds the private key block in data and accepts only an encrypted
// PKCS#8 one. Unencrypted and legacy "Proc-Type: 4,ENCRYPTED" keys are refused.
func classifyKeyBlock(data []byte) (*pem.Block, error) {
	var found *pem.Block
	for rest := data; ; {
		var b *pem.Block
		b, rest = pem.Decode(rest)
		if b == nil {
			break
		}
		if !strings.HasSuffix(b.Type, "PRIVATE KEY") {
			continue
		}
		switch {
		case b.Headers["Proc-Type"] != "" || b.Headers["DEK-Info"] != "":
			return nil, errors.New("legacy OpenSSL encrypted PEM key (Proc-Type: 4,ENCRYPTED) is not accepted; re-encrypt it as PKCS#8 (openssl pkcs8 -topk8) or use a PKCS#12 file")
		case b.Type != pemEncryptedKey:
			return nil, errors.New("the private key is unencrypted; fv only accepts an encrypted key (PKCS#12 or PKCS#8 ENCRYPTED PRIVATE KEY)")
		}
		if found == nil {
			found = b
		}
	}
	if found == nil {
		return nil, errors.New("no private key found in the PEM data")
	}
	return found, nil
}

// firstCertificate returns the first CERTIFICATE block parsed from the PEM inputs.
func firstCertificate(inputs ...[]byte) (*x509.Certificate, error) {
	for _, data := range inputs {
		for rest := data; ; {
			var b *pem.Block
			b, rest = pem.Decode(rest)
			if b == nil {
				break
			}
			if b.Type != "CERTIFICATE" {
				continue
			}
			c, err := x509.ParseCertificate(b.Bytes)
			if err != nil {
				return nil, fmt.Errorf("unreadable certificate: %w", err)
			}
			return c, nil
		}
	}
	return nil, errors.New("no certificate found in the PEM data")
}

// decryptPEMKey decrypts an encrypted PKCS#8 key block with pass.
func decryptPEMKey(b *pem.Block, pass string) (crypto.Signer, error) {
	pw := []byte(pass)
	defer zero(pw)
	k, err := pkcs8.ParsePKCS8PrivateKey(b.Bytes, pw)
	if err != nil {
		return nil, errors.New("cannot decrypt the private key with the given passphrase")
	}
	return asSigner(k)
}

// decryptP12 opens a PKCS#12 file and returns its key and leaf certificate.
func decryptP12(data []byte, pass string) (crypto.Signer, *x509.Certificate, error) {
	k, cert, err := pkcs12.Decode(data, pass)
	if err != nil {
		return nil, nil, errors.New("cannot open the PKCS#12 file with the given passphrase")
	}
	s, err := asSigner(k)
	if err != nil {
		return nil, nil, err
	}
	return s, cert, nil
}

func asSigner(k any) (crypto.Signer, error) {
	switch k := k.(type) {
	case *rsa.PrivateKey:
		return k, nil
	case *ecdsa.PrivateKey:
		return k, nil
	}
	return nil, fmt.Errorf("unsupported key type %T: need RSA or ECDSA", k)
}

// sameKey reports whether the private key belongs to the certificate.
func sameKey(k crypto.Signer, cert *x509.Certificate) bool {
	pub, ok := cert.PublicKey.(interface{ Equal(crypto.PublicKey) bool })
	return ok && pub.Equal(k.Public())
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

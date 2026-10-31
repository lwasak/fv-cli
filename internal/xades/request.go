package xades

import "crypto"

// AuthTokenNamespace is the XML namespace of the KSeF AuthTokenRequest.
const AuthTokenNamespace = "http://ksef.mf.gov.pl/auth/token/2.0" // #nosec G101 -- XML namespace URI, not a credential

// AuthTokenRequest is the content of the document KSeF asks to have signed.
type AuthTokenRequest struct {
	// Challenge is the value returned by POST /auth/challenge.
	Challenge string
	// ContextNIP is the NIP of the context the session is opened for (seller.nip).
	ContextNIP string
	// SubjectIdentifierType is how KSeF finds the subject, normally "certificateSubject".
	SubjectIdentifierType string
}

// Signer signs a digest. ECDSA signatures are raw r||s (the XML-DSig form), RSA ones are
// PKCS#1 v1.5. It is satisfied by *credentials.Credential, which decrypts the key only
// for the duration of the call.
type Signer interface {
	Sign(digest []byte, hash crypto.Hash) ([]byte, error)
}

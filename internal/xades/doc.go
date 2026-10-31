// Package xades builds the KSeF AuthTokenRequest and signs it as an enveloped XAdES-BES
// signature in pure Go.
//
// Every byte of the signed document is written here from fixed templates, directly in
// Exclusive XML Canonicalization form (no XML declaration, namespace declarations only
// where visibly used, attributes sorted, no self-closing tags, C14N escaping), so the
// digests are taken over exactly the bytes a verifier will canonicalise to. The package
// never sees key material: it asks a Signer to sign a digest.
package xades

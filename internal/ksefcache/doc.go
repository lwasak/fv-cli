// Package ksefcache is the local copy of what KSeF told fv about invoices: the metadata that
// fv list collects and the XML that fv show downloads. KSeF asks clients to keep such a copy
// instead of calling the API for every view, and an invoice in KSeF never changes, so
// entries are never removed and a downloaded XML is kept for good.
//
// The cache holds data about the seller's counterparties, so it is user-only like the
// archive. It is keyed by environment and context NIP and never mixes them:
//
//	<data>/ksef-cache/<env>/<nip>/index.json
//	<data>/ksef-cache/<env>/<nip>/xml/<ksefNumber>.xml
//	<data>/ksef-cache/<env>/<nip>/cache.lock
package ksefcache

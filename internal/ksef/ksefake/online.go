package ksefake

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// OnlineOptions configures EnableOnline.
type OnlineOptions struct {
	// Key is the RSA private key matching the SymmetricKeyEncryption certificate; the fake
	// uses it to unwrap each session's AES key, exactly as KSeF does. Required.
	Key *rsa.PrivateKey
	// CertDER, when set, is served at GET /security/public-key-certificates as the only
	// certificate, usage SymmetricKeyEncryption and KsefTokenEncryption, valid for its own
	// X.509 window. Leave nil to register that endpoint yourself.
	CertDER []byte
	// KeyID is served as the certificate's publicKeyId; when set, an open-session request
	// must carry the same publicKeyId.
	KeyID string
	// NIP is the seller NIP used in generated KSeF numbers (default 5833281095).
	NIP string
	// Bearer, when set, is the only access token the online routes accept (401 otherwise).
	Bearer string
	// Now is the clock for dates (default time.Now).
	Now func() time.Time
}

// Outcome scripts what the fake does with one POSTed invoice. The zero value accepts it:
// the status poll answers 200 with a generated KSeF number. Scripted outcomes are consumed
// one per POST /sessions/online/{ref}/invoices in the order given to Script; once the queue
// is empty every invoice is accepted (and a second invoice with the same hash as an
// accepted one is answered 440 duplicate, as KSeF does).
type Outcome struct {
	// Pending is how many status polls answer 150 before the final answer.
	Pending int
	// Code is the final invoice status: 200 (default), 440, 450, 430 ...
	Code        int
	Description string
	Details     []string
	// KSeFNumber overrides the generated number for Code 200 (it must be valid for ksef).
	KSeFNumber string
	// OriginalKSeFNumber and OriginalSessionRef are the 440 extensions.
	OriginalKSeFNumber string
	OriginalSessionRef string
	// PostStatus, when not 0, answers the POST with this HTTP status and a problem+json (or,
	// for 5xx, an empty) body; the invoice is not stored.
	PostStatus int
	// PostCode and PostDescription replace the generic exception code of a PostStatus answer.
	PostCode        int
	PostDescription string
	// Drop stores the invoice, then closes the connection without answering: the client
	// sees a transport error although KSeF has the invoice.
	Drop bool
	// DropLost closes the connection without storing the invoice (the request never arrived).
	DropLost bool
}

// Accept is the default outcome.
func Accept() Outcome { return Outcome{} }

// Reject answers the status poll with code (for example 450) and details.
func Reject(code int, description string, details ...string) Outcome {
	return Outcome{Code: code, Description: description, Details: details}
}

// Duplicate answers the status poll with 440 and the originalKsefNumber extension.
func Duplicate(originalKSeFNumber string) Outcome {
	return Outcome{Code: 440, OriginalKSeFNumber: originalKSeFNumber,
		OriginalSessionRef: "20261001-SO-0000000000-0000000000-00"}
}

// DropAfterReceive stores the invoice and loses the response.
func DropAfterReceive() Outcome { return Outcome{Drop: true} }

// DropBeforeReceive loses the request: nothing is stored.
func DropBeforeReceive() Outcome { return Outcome{DropLost: true} }

// PostError answers the POST itself with an HTTP error (400 gives a KSeF problem body, so
// the client can tell KSeF did not accept it; 5xx gives no KSeF body).
func PostError(status int) Outcome { return Outcome{PostStatus: status} }

// PostErrorCode answers the POST with a 400 that carries the KSeF exception code (for
// example 21184, session unavailable).
func PostErrorCode(status, code int, description string) Outcome {
	return Outcome{PostStatus: status, PostCode: code, PostDescription: description}
}

// AfterPolls returns o with n pending (150) status answers first.
func (o Outcome) AfterPolls(n int) Outcome { o.Pending = n; return o }

// Received is one invoice the fake stored.
type Received struct {
	SessionRef string
	InvoiceRef string
	// XML is the decrypted invoice.
	XML []byte
	// Hash and Size are the plain hash (Base64) and size the client declared (and the fake
	// verified).
	Hash string
	Size int
	// Status is the final status code of the scripted outcome.
	Status     int
	KSeFNumber string
	// Polls is how many GET status requests this invoice has had.
	Polls int
}

type onlineSession struct {
	ref        string
	key, iv    []byte
	validUntil time.Time
	created    time.Time
	closed     bool
	invoices   []*invoice
}

type invoice struct {
	Received
	out Outcome
}

// pending: the scripted number of 150 answers has not been used up yet.
func (i *invoice) pending() bool { return i.out.Pending > 0 && i.Polls <= i.out.Pending }

// Online is the online-session half of the fake. Create it with Fake.EnableOnline.
type Online struct {
	f    *Fake
	opts OnlineOptions

	mu         sync.Mutex
	sessions   map[string]*onlineSession
	order      []*onlineSession
	script     []Outcome
	seq        int
	validity   time.Duration
	upo        *Response
	violations []string
}

// EnableOnline registers the online-session routes of KSeF 2.0:
//
//	POST /sessions/online                                    open (RSA-OAEP key unwrap)
//	POST /sessions/online/{ref}/invoices                     send (decrypt, verify hashes and sizes)
//	POST /sessions/online/{ref}/close                        close (204)
//	GET  /sessions/{ref}                                     session status
//	GET  /sessions/{ref}/invoices[?pageSize=&x-continuation-token]  list
//	GET  /sessions/{ref}/invoices/{invRef}                   invoice status
//	GET  /sessions/{ref}/invoices/{invRef}/upo               UPO XML (accepted invoices)
//
// Anything the client does wrong (wrong formCode, key not wrapped for the served
// certificate, hash or size mismatch, wrong bearer) is answered 4xx and also recorded in
// Violations, so a test can assert there were none.
func (f *Fake) EnableOnline(opts OnlineOptions) *Online {
	if opts.Key == nil {
		panic("ksefake: OnlineOptions.Key is required")
	}
	if opts.NIP == "" {
		opts.NIP = "5833281095"
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	o := &Online{f: f, opts: opts, sessions: map[string]*onlineSession{}, validity: 12 * time.Hour}
	if opts.CertDER != nil {
		o.registerCert()
	}
	f.Handle("POST", "/sessions/online", o.open)
	f.HandlePattern("POST", "/sessions/online/*/invoices", o.send)
	f.HandlePattern("POST", "/sessions/online/*/close", o.closeSession)
	f.HandlePattern("GET", "/sessions/*", o.sessionStatus)
	f.HandlePattern("GET", "/sessions/*/invoices", o.list)
	f.HandlePattern("GET", "/sessions/*/invoices/*", o.invoiceStatus)
	f.HandlePattern("GET", "/sessions/*/invoices/*/upo", o.upoHandler)
	return o
}

func (o *Online) registerCert() {
	cert, err := x509.ParseCertificate(o.opts.CertDER)
	if err != nil {
		panic("ksefake: bad CertDER: " + err.Error())
	}
	entry := map[string]any{
		"certificate":   base64.StdEncoding.EncodeToString(o.opts.CertDER),
		"certificateId": "CERT-1",
		"validFrom":     cert.NotBefore.UTC().Format(time.RFC3339),
		"validTo":       cert.NotAfter.UTC().Format(time.RFC3339),
		"usage":         []string{"KsefTokenEncryption", "SymmetricKeyEncryption"},
	}
	if o.opts.KeyID != "" {
		entry["publicKeyId"] = o.opts.KeyID
	}
	b, _ := json.Marshal([]any{entry})
	o.f.Respond("GET", "/security/public-key-certificates", 200, string(b))
}

// Script queues outcomes for the next invoices.
func (o *Online) Script(outcomes ...Outcome) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.script = append(o.script, outcomes...)
}

// SetSessionValidity changes how long sessions opened from now on are valid (default 12 h).
func (o *Online) SetSessionValidity(d time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.validity = d
}

// SetValidUntil rewrites validUntil of an existing session (for example to the past).
func (o *Online) SetValidUntil(sessionRef string, t time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if s := o.sessions[sessionRef]; s != nil {
		s.validUntil = t
	}
}

// CloseSession closes a session on the server side (as KSeF does at validUntil).
func (o *Online) CloseSession(sessionRef string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if s := o.sessions[sessionRef]; s != nil {
		s.closed = true
	}
}

// SetUPO makes every UPO download answer resp instead of the generated UPO XML.
func (o *Online) SetUPO(resp Response) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.upo = &resp
}

// Invoices returns the stored invoices in arrival order.
func (o *Online) Invoices() []Received {
	o.mu.Lock()
	defer o.mu.Unlock()
	var out []Received
	for _, s := range o.order {
		for _, inv := range s.invoices {
			out = append(out, inv.Received)
		}
	}
	return out
}

// SessionRefs returns the references of the opened sessions in order.
func (o *Online) SessionRefs() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	var out []string
	for _, s := range o.order {
		out = append(out, s.ref)
	}
	return out
}

// SessionKey returns the unwrapped AES key and IV of a session (a test of "what the client
// generated"); nil when the session is unknown.
func (o *Online) SessionKey(sessionRef string) (key, iv []byte) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if s := o.sessions[sessionRef]; s != nil {
		return append([]byte(nil), s.key...), append([]byte(nil), s.iv...)
	}
	return nil, nil
}

// Closed reports whether the session was closed.
func (o *Online) Closed(sessionRef string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	s := o.sessions[sessionRef]
	return s != nil && s.closed
}

// Violations lists protocol errors the client made (each was answered 4xx).
func (o *Online) Violations() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.violations...)
}

// --- helpers ---

type apiErr struct {
	Code        int      `json:"code"`
	Description string   `json:"description"`
	Details     []string `json:"details,omitempty"`
}

func problem(status int, title, detail string, errs ...apiErr) Response {
	b, _ := json.Marshal(map[string]any{
		"title": title, "status": status, "detail": detail, "errors": errs,
		"instance": "ksefake", "timestamp": "2026-10-30T12:00:00Z", "traceId": "ksefake-trace",
	})
	return Response{Status: status, Body: string(b), ContentType: "application/problem+json"}
}

func (o *Online) violation(format string, a ...any) string {
	msg := fmt.Sprintf(format, a...)
	o.violations = append(o.violations, msg)
	return msg
}

// bad records a violation and answers 400.
func (o *Online) bad(code int, format string, a ...any) Response {
	msg := o.violation(format, a...)
	return problem(400, "Bad Request", "Żądanie jest nieprawidłowe.", apiErr{Code: code, Description: "Błąd walidacji danych wejściowych.", Details: []string{msg}})
}

// auth checks the bearer; it must be called with o.mu held.
func (o *Online) auth(r Request) *Response {
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if got == "" || (o.opts.Bearer != "" && got != o.opts.Bearer) {
		o.violation("%s %s: missing or wrong bearer token", r.Method, r.Path)
		p := problem(401, "Unauthorized", "Wymagane jest uwierzytelnienie.")
		return &p
	}
	return nil
}

func (o *Online) newRef(kind string) string {
	o.seq++
	return fmt.Sprintf("%s-%s-%010X-%010X-%02X", o.opts.Now().UTC().Format("20060102"), kind, 0x2C3E6C8000+o.seq, 0xB675CF5D68+o.seq, o.seq&0xFF)
}

func crc8(s string) byte {
	var c byte
	for i := 0; i < len(s); i++ {
		c ^= s[i]
		for range 8 {
			if c&0x80 != 0 {
				c = c<<1 ^ 0x07
			} else {
				c <<= 1
			}
		}
	}
	return c
}

func (o *Online) newKSeFNumber() string {
	o.seq++
	base := fmt.Sprintf("%s-%s-%012X", o.opts.NIP, o.opts.Now().UTC().Format("20060102"), 0x0100001AF629+o.seq)
	return fmt.Sprintf("%s-%02X", base, crc8(base))
}

// --- handlers ---

func (o *Online) open(r Request) Response {
	o.mu.Lock()
	defer o.mu.Unlock()
	if p := o.auth(r); p != nil {
		return *p
	}
	var req struct {
		FormCode struct {
			SystemCode    string `json:"systemCode"`
			SchemaVersion string `json:"schemaVersion"`
			Value         string `json:"value"`
		} `json:"formCode"`
		Encryption struct {
			EncryptedSymmetricKey string `json:"encryptedSymmetricKey"`
			InitializationVector  string `json:"initializationVector"`
			PublicKeyID           string `json:"publicKeyId"`
		} `json:"encryption"`
	}
	if err := json.Unmarshal(r.Body, &req); err != nil {
		return o.bad(21405, "open request is not JSON: %v", err)
	}
	if fc := req.FormCode; fc.SystemCode != "FA (3)" || fc.SchemaVersion != "1-0E" || fc.Value != "FA" {
		return o.bad(21405, "unsupported formCode %+v", fc)
	}
	if o.opts.KeyID != "" && req.Encryption.PublicKeyID != o.opts.KeyID {
		return o.bad(21405, "publicKeyId %q, want %q", req.Encryption.PublicKeyID, o.opts.KeyID)
	}
	wrapped, err := base64.StdEncoding.DecodeString(req.Encryption.EncryptedSymmetricKey)
	if err != nil {
		return o.bad(21405, "encryptedSymmetricKey is not Base64")
	}
	key, err := rsa.DecryptOAEP(sha256.New(), nil, o.opts.Key, wrapped, nil)
	if err != nil || len(key) != 32 {
		return o.bad(21405, "the symmetric key does not unwrap with RSA-OAEP SHA-256 to 32 bytes (err=%v, len=%d)", err, len(key))
	}
	iv, err := base64.StdEncoding.DecodeString(req.Encryption.InitializationVector)
	if err != nil || len(iv) != 16 {
		return o.bad(21405, "the IV is not 16 bytes of Base64")
	}
	now := o.opts.Now()
	s := &onlineSession{ref: o.newRef("SO"), key: key, iv: iv, created: now, validUntil: now.Add(o.validity)}
	o.sessions[s.ref] = s
	o.order = append(o.order, s)
	b, _ := json.Marshal(map[string]any{"referenceNumber": s.ref, "validUntil": s.validUntil.UTC().Format(time.RFC3339Nano)})
	return Response{Status: 201, Body: string(b)}
}

func (o *Online) send(r Request) Response {
	o.mu.Lock()
	defer o.mu.Unlock()
	if p := o.auth(r); p != nil {
		return *p
	}
	s := o.sessions[r.Params[0]]
	if s == nil {
		return o.bad(21166, "unknown session %q", r.Params[0])
	}
	if s.closed {
		return o.bad(21180, "session %s is closed", s.ref)
	}
	var out Outcome
	if len(o.script) > 0 {
		out, o.script = o.script[0], o.script[1:]
	}
	if out.DropLost {
		return Response{Drop: true}
	}
	if out.PostStatus != 0 {
		if out.PostStatus >= 500 {
			return Response{Status: out.PostStatus, Body: "upstream failure", ContentType: "text/plain"}
		}
		return problem(out.PostStatus, http.StatusText(out.PostStatus), "scripted by ksefake",
			apiErr{Code: cmpOr(out.PostCode, out.PostStatus), Description: cmpOrS(out.PostDescription, "scripted error")})
	}
	var req struct {
		InvoiceHash          string `json:"invoiceHash"`
		InvoiceSize          int    `json:"invoiceSize"`
		EncryptedHash        string `json:"encryptedInvoiceHash"`
		EncryptedSize        int    `json:"encryptedInvoiceSize"`
		EncryptedContent     string `json:"encryptedInvoiceContent"`
		OfflineMode          bool   `json:"offlineMode"`
		HashOfCorrectedInvoi string `json:"hashOfCorrectedInvoice"`
	}
	if err := json.Unmarshal(r.Body, &req); err != nil {
		return o.bad(21405, "send request is not JSON: %v", err)
	}
	ct, err := base64.StdEncoding.DecodeString(req.EncryptedContent)
	if err != nil {
		return o.bad(21405, "encryptedInvoiceContent is not Base64")
	}
	if req.EncryptedSize != len(ct) {
		return o.bad(21405, "encryptedInvoiceSize %d, content is %d bytes", req.EncryptedSize, len(ct))
	}
	if h := hash(ct); h != req.EncryptedHash {
		return o.bad(21405, "encryptedInvoiceHash %q, content hashes to %q", req.EncryptedHash, h)
	}
	if req.OfflineMode {
		return o.bad(21405, "offlineMode must be false for an online send")
	}
	inv := &invoice{out: out}
	inv.SessionRef, inv.Hash, inv.Size = s.ref, req.InvoiceHash, req.InvoiceSize
	plain, derr := decrypt(s.key, s.iv, ct)
	if derr == nil {
		if len(plain) != req.InvoiceSize {
			return o.bad(21405, "invoiceSize %d, decrypted invoice is %d bytes", req.InvoiceSize, len(plain))
		}
		if h := hash(plain); h != req.InvoiceHash {
			return o.bad(21405, "invoiceHash %q, decrypted invoice hashes to %q", req.InvoiceHash, h)
		}
		inv.XML = plain
	} else {
		o.violation("invoice does not decrypt with the session key: %v", derr)
		inv.out = Reject(435, "Błąd odszyfrowania pliku")
	}
	inv.InvoiceRef = o.newRef("EE")
	o.finalise(inv)
	s.invoices = append(s.invoices, inv)
	if out.Drop {
		return Response{Drop: true}
	}
	b, _ := json.Marshal(map[string]any{"referenceNumber": inv.InvoiceRef})
	return Response{Status: 202, Body: string(b)}
}

func hash(b []byte) string {
	sum := sha256.Sum256(b)
	return base64.StdEncoding.EncodeToString(sum[:])
}

func decrypt(key, iv, ct []byte) ([]byte, error) {
	if len(ct) == 0 || len(ct)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("ciphertext is %d bytes, not a multiple of the block size", len(ct))
	}
	blk, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(ct))
	cipher.NewCBCDecrypter(blk, iv).CryptBlocks(out, ct)
	pad := int(out[len(out)-1])
	if pad == 0 || pad > aes.BlockSize {
		return nil, fmt.Errorf("bad PKCS#7 padding byte %d", pad)
	}
	for _, b := range out[len(out)-pad:] {
		if int(b) != pad {
			return nil, fmt.Errorf("bad PKCS#7 padding")
		}
	}
	return out[:len(out)-pad], nil
}

var defaultDescriptions = map[int]string{
	200: "Sukces",
	405: "Przetwarzanie anulowane z powodu błędu sesji",
	410: "Nieprawidłowy zakres uprawnień",
	430: "Błąd weryfikacji pliku faktury",
	435: "Błąd odszyfrowania pliku",
	440: "Duplikat faktury",
	450: "Błąd weryfikacji semantyki dokumentu faktury",
	500: "Nieznany błąd",
}

// finalise decides the final status of inv. o.mu must be held.
func (o *Online) finalise(inv *invoice) {
	out := inv.out
	if out.Code == 0 {
		out.Code = 200
		if orig := o.acceptedWithHash(inv.Hash); orig != nil && inv.XML != nil {
			out = Duplicate(orig.KSeFNumber)
			out.OriginalSessionRef = orig.SessionRef
			out.Pending = inv.out.Pending
		}
	}
	if out.Description == "" {
		out.Description = defaultDescriptions[out.Code]
	}
	if out.Code == 200 {
		if out.KSeFNumber == "" {
			out.KSeFNumber = o.newKSeFNumber()
		}
		inv.KSeFNumber = out.KSeFNumber
	}
	inv.out = out
	inv.Status = out.Code
}

func (o *Online) acceptedWithHash(h string) *invoice {
	for _, s := range o.order {
		for _, inv := range s.invoices {
			if inv.Hash == h && inv.Status == 200 {
				return inv
			}
		}
	}
	return nil
}

// statusJSON renders the SessionInvoiceStatusResponse. When advance is set it counts one
// more poll. o.mu must be held.
func (o *Online) statusJSON(s *onlineSession, inv *invoice, ordinal int, advance bool) map[string]any {
	if advance {
		inv.Polls++
	}
	now := o.opts.Now().UTC().Format(time.RFC3339Nano)
	m := map[string]any{
		"ordinalNumber": ordinal, "referenceNumber": inv.InvoiceRef, "invoiceHash": inv.Hash,
		"invoicingDate": now, "invoicingMode": "Online",
	}
	if inv.pending() {
		m["status"] = map[string]any{"code": 150, "description": "Trwa przetwarzanie"}
		return m
	}
	st := map[string]any{"code": inv.out.Code, "description": inv.out.Description}
	if len(inv.out.Details) > 0 {
		st["details"] = inv.out.Details
	}
	if inv.out.Code == 440 {
		st["extensions"] = map[string]string{
			"originalKsefNumber":             inv.out.OriginalKSeFNumber,
			"originalSessionReferenceNumber": inv.out.OriginalSessionRef,
		}
	}
	m["status"] = st
	if inv.out.Code == 200 {
		m["ksefNumber"] = inv.KSeFNumber
		m["acquisitionDate"] = now
		m["permanentStorageDate"] = now
		m["upoDownloadUrl"] = "https://upo.invalid/" + inv.InvoiceRef
	}
	return m
}

func jsonResp(status int, v any) Response {
	b, _ := json.Marshal(v)
	return Response{Status: status, Body: string(b)}
}

func (o *Online) lookup(r Request) (*onlineSession, *Response) {
	if p := o.auth(r); p != nil {
		return nil, p
	}
	s := o.sessions[r.Params[0]]
	if s == nil {
		p := problem(400, "Bad Request", "Żądanie jest nieprawidłowe.", apiErr{Code: 21166, Description: "Nie znaleziono sesji.", Details: []string{r.Params[0]}})
		return nil, &p
	}
	return s, nil
}

func (o *Online) closeSession(r Request) Response {
	o.mu.Lock()
	defer o.mu.Unlock()
	s, errResp := o.lookup(r)
	if errResp != nil {
		return *errResp
	}
	s.closed = true
	return Response{Status: 204}
}

func (o *Online) sessionStatus(r Request) Response {
	o.mu.Lock()
	defer o.mu.Unlock()
	s, errResp := o.lookup(r)
	if errResp != nil {
		return *errResp
	}
	ok := 0
	for _, inv := range s.invoices {
		if inv.out.Code == 200 {
			ok++
		}
	}
	st := map[string]any{"code": 100, "description": "Sesja interaktywna otwarta"}
	switch {
	case s.closed && len(s.invoices) == 0:
		st = map[string]any{"code": 440, "description": "Sesja anulowana", "details": []string{"Nie przesłano faktur"}}
	case s.closed:
		st = map[string]any{"code": 200, "description": "Sesja interaktywna przetworzona pomyślnie"}
	}
	return jsonResp(200, map[string]any{
		"status": st, "dateCreated": s.created.UTC().Format(time.RFC3339Nano),
		"dateUpdated":  o.opts.Now().UTC().Format(time.RFC3339Nano),
		"validUntil":   s.validUntil.UTC().Format(time.RFC3339Nano),
		"invoiceCount": len(s.invoices), "successfulInvoiceCount": ok, "failedInvoiceCount": len(s.invoices) - ok,
	})
}

func (o *Online) list(r Request) Response {
	o.mu.Lock()
	defer o.mu.Unlock()
	s, errResp := o.lookup(r)
	if errResp != nil {
		return *errResp
	}
	size := 10
	offset := 0
	for _, kv := range strings.Split(r.Query, "&") {
		if v, ok := strings.CutPrefix(kv, "pageSize="); ok {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				size = n
			}
		}
	}
	if tok := r.Header.Get("x-continuation-token"); tok != "" {
		n, err := strconv.Atoi(tok)
		if err != nil || n < 0 {
			return o.bad(21405, "bad continuation token %q", tok)
		}
		offset = n
	}
	end := min(offset+size, len(s.invoices))
	items := []map[string]any{}
	for i := min(offset, end); i < end; i++ {
		items = append(items, o.statusJSON(s, s.invoices[i], i+1, false))
	}
	resp := map[string]any{"invoices": items}
	if end < len(s.invoices) {
		resp["continuationToken"] = strconv.Itoa(end)
	}
	return jsonResp(200, resp)
}

func (o *Online) find(r Request) (*onlineSession, *invoice, int, *Response) {
	s, errResp := o.lookup(r)
	if errResp != nil {
		return nil, nil, 0, errResp
	}
	for i, inv := range s.invoices {
		if inv.InvoiceRef == r.Params[1] {
			return s, inv, i + 1, nil
		}
	}
	p := problem(400, "Bad Request", "Żądanie jest nieprawidłowe.", apiErr{Code: 21166, Description: "Nie znaleziono faktury.", Details: []string{r.Params[1]}})
	return nil, nil, 0, &p
}

func (o *Online) invoiceStatus(r Request) Response {
	o.mu.Lock()
	defer o.mu.Unlock()
	s, inv, n, errResp := o.find(r)
	if errResp != nil {
		return *errResp
	}
	return jsonResp(200, o.statusJSON(s, inv, n, true))
}

func (o *Online) upoHandler(r Request) Response {
	o.mu.Lock()
	defer o.mu.Unlock()
	_, inv, _, errResp := o.find(r)
	if errResp != nil {
		return *errResp
	}
	if o.upo != nil {
		return *o.upo
	}
	if inv.out.Code != 200 || inv.pending() {
		return problem(400, "Bad Request", "UPO jest niedostępne.", apiErr{Code: 21173, Description: "Faktura nie została przyjęta."})
	}
	xml := UPOXML(inv.KSeFNumber)
	return Response{Status: 200, Body: xml, ContentType: "application/xml",
		Header: map[string]string{"x-ms-meta-hash": hash([]byte(xml))}}
}

// UPOXML is the minimal UPO document the fake serves for an accepted invoice.
func UPOXML(ksefNumber string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<Potwierdzenie xmlns="http://upo.schematy.mf.gov.pl/KSeF/v4-3"><NazwaPodmiotuPrzyjmujacego>Ministerstwo Finansów (ksefake)</NazwaPodmiotuPrzyjmujacego><Dokument><NumerKSeFDokumentu>` + ksefNumber + `</NumerKSeFDokumentu></Dokument></Potwierdzenie>
`
}

// UPOFor returns the UPO the fake serves for the invoice with the given reference.
func (o *Online) UPOFor(invoiceRef string) []byte {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, s := range o.order {
		for _, inv := range s.invoices {
			if inv.InvoiceRef == invoiceRef && inv.out.Code == 200 {
				return []byte(UPOXML(inv.KSeFNumber))
			}
		}
	}
	return nil
}

func cmpOr(a, b int) int {
	if a != 0 {
		return a
	}
	return b
}

func cmpOrS(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

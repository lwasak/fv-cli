package ksefake_test

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/lwasak/fv-cli/internal/credentials/credtest"
	"github.com/lwasak/fv-cli/internal/ksef/ksefake"
)

// rawClient speaks the wire protocol by hand so the fake is tested without package ksef.
type rawClient struct {
	t        *testing.T
	f        *ksefake.Fake
	key, iv  []byte
	sessions string
}

func post(t *testing.T, f *ksefake.Fake, path string, body any) (int, []byte) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := newReq("POST", f.URL+path, b)
	resp, err := f.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.Bytes()
}

func setup(t *testing.T) (*ksefake.Fake, *ksefake.Online, *rawClient) {
	t.Helper()
	cert := credtest.New(t, credtest.Spec{RSA: true})
	priv := cert.Key.(*rsa.PrivateKey)
	f := ksefake.New(t)
	on := f.EnableOnline(ksefake.OnlineOptions{Key: priv, CertDER: cert.DER})
	rc := &rawClient{t: t, f: f, key: bytes.Repeat([]byte{7}, 32), iv: bytes.Repeat([]byte{9}, 16)}
	wrapped, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, &priv.PublicKey, rc.key, nil)
	if err != nil {
		t.Fatal(err)
	}
	code, body := post(t, f, "/sessions/online", map[string]any{
		"formCode": map[string]string{"systemCode": "FA (3)", "schemaVersion": "1-0E", "value": "FA"},
		"encryption": map[string]string{
			"encryptedSymmetricKey": base64.StdEncoding.EncodeToString(wrapped),
			"initializationVector":  base64.StdEncoding.EncodeToString(rc.iv),
		},
	})
	if code != 201 {
		t.Fatalf("open = %d %s", code, body)
	}
	var out struct{ ReferenceNumber string }
	_ = json.Unmarshal(body, &out)
	rc.sessions = out.ReferenceNumber
	return f, on, rc
}

func (rc *rawClient) send(plain []byte, mutate func(m map[string]any)) (int, []byte) {
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	buf := append(append([]byte{}, plain...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	blk, _ := aes.NewCipher(rc.key)
	ct := make([]byte, len(buf))
	cipher.NewCBCEncrypter(blk, rc.iv).CryptBlocks(ct, buf)
	ph, ch := sha256.Sum256(plain), sha256.Sum256(ct)
	m := map[string]any{
		"invoiceHash": base64.StdEncoding.EncodeToString(ph[:]), "invoiceSize": len(plain),
		"encryptedInvoiceHash": base64.StdEncoding.EncodeToString(ch[:]), "encryptedInvoiceSize": len(ct),
		"encryptedInvoiceContent": base64.StdEncoding.EncodeToString(ct),
	}
	if mutate != nil {
		mutate(m)
	}
	return post(rc.t, rc.f, "/sessions/online/"+rc.sessions+"/invoices", m)
}

func TestOnlineFake_RejectsEveryWrongHashAndSize(t *testing.T) {
	for name, mutate := range map[string]func(map[string]any){
		"plain hash":     func(m map[string]any) { m["invoiceHash"] = "AAAA" },
		"plain size":     func(m map[string]any) { m["invoiceSize"] = 1 },
		"encrypted hash": func(m map[string]any) { m["encryptedInvoiceHash"] = "AAAA" },
		"encrypted size": func(m map[string]any) { m["encryptedInvoiceSize"] = 1 },
		"offline mode":   func(m map[string]any) { m["offlineMode"] = true },
	} {
		t.Run(name, func(t *testing.T) {
			_, on, rc := setup(t)
			code, _ := rc.send([]byte("<Faktura/>"), mutate)
			if code != 400 {
				t.Errorf("status = %d, want 400", code)
			}
			if len(on.Violations()) != 1 || len(on.Invoices()) != 0 {
				t.Errorf("violations = %v, invoices = %d", on.Violations(), len(on.Invoices()))
			}
		})
	}
}

func TestOnlineFake_AcceptsCorrectSendAndDecrypts(t *testing.T) {
	_, on, rc := setup(t)
	code, body := rc.send([]byte("<Faktura/>"), nil)
	if code != 202 || !strings.Contains(string(body), "-EE-") {
		t.Fatalf("send = %d %s", code, body)
	}
	if got := on.Invoices(); len(got) != 1 || string(got[0].XML) != "<Faktura/>" || len(on.Violations()) != 0 {
		t.Errorf("invoices = %+v, violations = %v", got, on.Violations())
	}
}

func TestOnlineFake_WrongKeyWrapFailsOpen(t *testing.T) {
	cert := credtest.New(t, credtest.Spec{RSA: true})
	other := credtest.New(t, credtest.Spec{RSA: true})
	f := ksefake.New(t)
	on := f.EnableOnline(ksefake.OnlineOptions{Key: cert.Key.(*rsa.PrivateKey)})
	wrapped, _ := rsa.EncryptOAEP(sha256.New(), rand.Reader, &other.Key.(*rsa.PrivateKey).PublicKey, bytes.Repeat([]byte{1}, 32), nil)
	code, _ := post(t, f, "/sessions/online", map[string]any{
		"formCode":   map[string]string{"systemCode": "FA (3)", "schemaVersion": "1-0E", "value": "FA"},
		"encryption": map[string]string{"encryptedSymmetricKey": base64.StdEncoding.EncodeToString(wrapped), "initializationVector": base64.StdEncoding.EncodeToString(make([]byte, 16))},
	})
	if code != 400 || len(on.Violations()) != 1 {
		t.Errorf("code = %d, violations = %v", code, on.Violations())
	}
}

func TestOnlineFake_CountsAndInject(t *testing.T) {
	f, _, rc := setup(t)
	rc.send([]byte("<a/>"), nil)
	if f.Count("POST", "/sessions/online") != 1 || f.CountPrefix("/sessions/") != 2 || f.Counts()["POST /sessions/online"] != 1 {
		t.Errorf("counts = %v", f.Counts())
	}
	f.Inject("POST", "/sessions/online/"+rc.sessions, 1, ksefake.Response{Status: 503})
	if code, _ := rc.send([]byte("<b/>"), nil); code != 503 {
		t.Errorf("injected status = %d", code)
	}
	if code, _ := rc.send([]byte("<b/>"), nil); code != 202 {
		t.Errorf("after the injection: %d", code)
	}
}

func newReq(method, url string, body []byte) (*http.Request, error) {
	r, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err == nil {
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer t")
	}
	return r, err
}

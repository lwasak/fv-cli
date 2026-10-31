package ksef

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lwasak/fv-cli/internal/ksef/ksefake"
)

var downloadXML = []byte("<?xml version=\"1.0\"?>\n<Faktura><Fa><P_2>FV/1</P_2></Fa></Faktura>\n")

func TestDownloadInvoice_ReturnsExactBytesAndHash(t *testing.T) {
	fx := newQueryFixture(t, 0)
	n := fx.q.Add(ksefake.QMeta{InvoiceNumber: "FV/1", IssueDate: "2026-09-15", SellerNIP: queryNIP}, downloadXML)
	got, hash, err := fx.c.DownloadInvoice(context.Background(), fx.s, n)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := InvoiceHash(downloadXML)
	if string(got) != string(downloadXML) || hash != want {
		t.Errorf("got %q hash %q", got, hash)
	}
	r := fx.f.Requests()[0]
	if r.Method != "GET" || r.Path != "/invoices/ksef/"+n || r.Header.Get("Accept") != "application/xml" || r.Header.Get("Authorization") != "Bearer "+queryBearer {
		t.Errorf("request %+v", r)
	}
}

func TestDownloadInvoice_InvalidNumberSendsNothing(t *testing.T) {
	fx := newQueryFixture(t, 0)
	for _, n := range []string{"", "../etc/passwd", "5833281095-20260915-0100001AF629-00", "x"} {
		if _, _, err := fx.c.DownloadInvoice(context.Background(), fx.s, n); !errors.Is(err, ErrBadKSeFNumber) {
			t.Errorf("%q: err = %v", n, err)
		}
	}
	if n := len(fx.f.Requests()); n != 0 {
		t.Errorf("%d requests", n)
	}
}

func TestDownloadInvoice_HashMismatchIsRefused(t *testing.T) {
	fx := newQueryFixture(t, 0)
	n := fx.q.Add(ksefake.QMeta{InvoiceNumber: "FV/1", IssueDate: "2026-09-15", SellerNIP: queryNIP}, downloadXML)
	fx.f.Handle("GET", "/invoices/ksef/"+n, func(ksefake.Request) ksefake.Response {
		return ksefake.Response{Body: string(downloadXML), ContentType: "application/xml",
			Header: map[string]string{"x-ms-meta-hash": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}}
	})
	got, _, err := fx.c.DownloadInvoice(context.Background(), fx.s, n)
	if !errors.Is(err, ErrDownloadHash) || got != nil {
		t.Errorf("got %q, err %v", got, err)
	}
}

func TestDownloadInvoice_EmptyBodyIsAnError(t *testing.T) {
	fx := newQueryFixture(t, 0)
	n := fx.q.Add(ksefake.QMeta{InvoiceNumber: "FV/1", IssueDate: "2026-09-15", SellerNIP: queryNIP}, downloadXML)
	fx.f.Handle("GET", "/invoices/ksef/"+n, func(ksefake.Request) ksefake.Response {
		return ksefake.Response{ContentType: "application/xml"}
	})
	if _, _, err := fx.c.DownloadInvoice(context.Background(), fx.s, n); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("err = %v", err)
	}
}

func TestDownloadInvoice_NoSuchAndNotYetAvailable(t *testing.T) {
	fx := newQueryFixture(t, 0)
	n := fx.q.Add(ksefake.QMeta{InvoiceNumber: "FV/1", IssueDate: "2026-09-15", SellerNIP: queryNIP}, downloadXML)
	fx.q.SetNotYetAvailable(n, 1)
	_, _, err := fx.c.DownloadInvoice(context.Background(), fx.s, n)
	if !IsNotYetAvailable(err) || IsNoSuchInvoice(err) {
		t.Errorf("first: %v", err)
	}
	if _, _, err := fx.c.DownloadInvoice(context.Background(), fx.s, n); err != nil {
		t.Errorf("second: %v", err)
	}
	other := "5833281095-20260915-0100001AF629-00"
	// build a number with valid check digits that the fake does not know
	for _, c := range []string{"5833281095-20260916-0200002AF999"} {
		other = c + "-" + checkDigits(c)
	}
	_, _, err = fx.c.DownloadInvoice(context.Background(), fx.s, other)
	if !IsNoSuchInvoice(err) || IsNotYetAvailable(err) {
		t.Errorf("unknown: %v", err)
	}
}

func checkDigits(base string) string {
	const hex = "0123456789ABCDEF"
	c := crc8(base)
	return string([]byte{hex[c>>4], hex[c&15]})
}

func TestDownloadInvoice_RetriesAn429Get(t *testing.T) {
	fx := newQueryFixture(t, 0)
	n := fx.q.Add(ksefake.QMeta{InvoiceNumber: "FV/1", IssueDate: "2026-09-15", SellerNIP: queryNIP}, downloadXML)
	fx.f.Inject("GET", "/invoices/ksef/", 1, ksefake.Response{Status: 429, Header: map[string]string{"Retry-After": "1"},
		Body: `{"status":{"code":429,"description":"Too Many Requests"}}`})
	fx.c.SetSleep(func(context.Context, time.Duration) error { return nil })
	if _, _, err := fx.c.DownloadInvoice(context.Background(), fx.s, n); err != nil {
		t.Fatal(err)
	}
	if c := fx.f.CountPrefix("/invoices/ksef/"); c != 2 {
		t.Errorf("%d requests", c)
	}
}

func TestDownloadInvoice_ForbiddenHint(t *testing.T) {
	fx := newQueryFixture(t, 0)
	n := fx.q.Add(ksefake.QMeta{InvoiceNumber: "FV/1", IssueDate: "2026-09-15", SellerNIP: queryNIP}, downloadXML)
	fx.f.Handle("GET", "/invoices/ksef/"+n, func(ksefake.Request) ksefake.Response {
		return ksefake.Response{Status: 403, ContentType: "application/problem+json", Body: `{"title":"Forbidden","status":403}`}
	})
	_, _, err := fx.c.DownloadInvoice(context.Background(), fx.s, n)
	if !IsForbidden(err) || !strings.Contains(err.Error(), "InvoiceRead") {
		t.Errorf("err = %v", err)
	}
}

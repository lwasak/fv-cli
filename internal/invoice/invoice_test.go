package invoice

import (
	"strings"
	"testing"
	"time"

	"github.com/lwasak/fv-cli/internal/money"
)

func d(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := ParseDate(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func fmtD(t time.Time) string { return t.Format("2006-01-02 15:04 MST") }

func TestParseDate(t *testing.T) {
	for _, s := range []string{"2026-02-30", "2026-2-3", "", "26-01-01", "2026-13-01", "2026/01/01", "2026-01-01 "} {
		if _, err := ParseDate(s); err == nil {
			t.Errorf("ParseDate(%q) accepted", s)
		}
	}
	got := d(t, "2028-02-29")
	if fmtD(got) != "2028-02-29 00:00 CET" || got.Location() != Warsaw() {
		t.Errorf("got %s", fmtD(got))
	}
}

func TestToday(t *testing.T) {
	// 23:30 UTC on 30 Oct is already 31 Oct in Warsaw (CET, winter after 25 Oct).
	now := time.Date(2026, 10, 30, 23, 30, 0, 0, time.UTC)
	if got := fmtD(Today(now)); got != "2026-10-31 00:00 CET" {
		t.Errorf("got %s", got)
	}
}

func TestSaleDate(t *testing.T) {
	tests := []struct{ issue, rule, want string }{
		{"2027-01-31", "issue_date", "2027-01-31"},
		{"2027-01-31", "", "2027-01-31"},
		{"2027-01-31", "last_day_of_month", "2027-01-31"},
		{"2027-01-31", "last_day_of_previous_month", "2026-12-31"},
		{"2027-02-10", "last_day_of_month", "2027-02-28"},
		{"2028-02-10", "last_day_of_month", "2028-02-29"},
		{"2028-03-01", "last_day_of_previous_month", "2028-02-29"},
		{"2027-03-01", "last_day_of_previous_month", "2027-02-28"},
		{"2026-12-31", "last_day_of_month", "2026-12-31"},
		{"2026-12-15", "last_day_of_month", "2026-12-31"},
		{"2026-12-31", "last_day_of_previous_month", "2026-11-30"},
		{"2027-01-01", "last_day_of_previous_month", "2026-12-31"},
		{"2026-04-01", "last_day_of_month", "2026-04-30"},
		{"2026-03-28", "last_day_of_month", "2026-03-31"}, // DST change on 29 Mar
		{"2026-10-31", "issue_date", "2026-10-31"},
		{"2026-10-01", "last_day_of_month", "2026-10-31"},
	}
	for _, tc := range tests {
		got, err := SaleDate(d(t, tc.issue), tc.rule)
		if err != nil {
			t.Errorf("%s %s: %v", tc.issue, tc.rule, err)
			continue
		}
		if got.Format("2006-01-02") != tc.want || got.Hour() != 0 || got.Location() != Warsaw() {
			t.Errorf("%s %s: got %s want %s", tc.issue, tc.rule, fmtD(got), tc.want)
		}
	}
	if _, err := SaleDate(d(t, "2026-10-01"), "next_week"); err == nil || !strings.Contains(err.Error(), "next_week") {
		t.Errorf("unknown rule: %v", err)
	}
	if _, err := SaleDate(time.Time{}, "issue_date"); err == nil {
		t.Error("zero issue accepted")
	}
}

func TestSaleDateNormalizesInstant(t *testing.T) {
	// A UTC instant that is already the next day in Warsaw.
	in := time.Date(2026, 12, 30, 23, 30, 0, 0, time.UTC)
	got, err := SaleDate(in, "last_day_of_month")
	if err != nil || got.Format("2006-01-02") != "2026-12-31" {
		t.Errorf("got %v %v", got, err)
	}
	got, _ = SaleDate(in, "issue_date")
	if got.Format("2006-01-02") != "2026-12-31" {
		t.Errorf("issue_date got %v", got)
	}
}

func TestDueDate(t *testing.T) {
	tests := []struct {
		issue string
		days  int
		want  string
	}{
		{"2026-10-31", 0, "2026-10-31"},
		{"2026-10-31", 14, "2026-11-14"},
		{"2026-12-31", 1, "2027-01-01"},
		{"2026-12-20", 14, "2027-01-03"},
		{"2026-12-31", 30, "2027-01-30"},
		{"2027-02-20", 14, "2027-03-06"},
		{"2028-02-20", 14, "2028-03-05"},
		{"2028-02-28", 1, "2028-02-29"},
		{"2027-02-28", 1, "2027-03-01"},
		{"2026-03-20", 14, "2026-04-03"}, // across DST start
		{"2026-10-20", 14, "2026-11-03"}, // across DST end
		{"2026-12-31", 365, "2027-12-31"},
		{"2027-12-31", 366, "2028-12-31"},
	}
	for _, tc := range tests {
		got, err := DueDate(d(t, tc.issue), tc.days)
		if err != nil {
			t.Fatal(err)
		}
		if got.Format("2006-01-02") != tc.want || got.Hour() != 0 {
			t.Errorf("%s+%d: got %s want %s", tc.issue, tc.days, fmtD(got), tc.want)
		}
	}
	for _, days := range []int{-1, 3651} {
		if _, err := DueDate(d(t, "2026-01-01"), days); err == nil {
			t.Errorf("days %d accepted", days)
		}
	}
}

func TestRenderTemplate(t *testing.T) {
	tests := []struct{ tpl, sale, want string }{
		{"Usługi IT {month_name} {year}", "2026-10-31", "Usługi IT październik 2026"},
		{"IT services for {month_name_en} {year}", "2026-10-31", "IT services for October 2026"},
		{"{year}/{month_name}", "2027-01-15", "2027/styczeń"},
		{"{month_name}", "2027-02-28", "luty"},
		{"{month_name}", "2027-03-01", "marzec"},
		{"{month_name}", "2027-04-01", "kwiecień"},
		{"{month_name}", "2027-05-01", "maj"},
		{"{month_name}", "2027-06-01", "czerwiec"},
		{"{month_name}", "2027-07-01", "lipiec"},
		{"{month_name}", "2027-08-01", "sierpień"},
		{"{month_name}", "2027-09-01", "wrzesień"},
		{"{month_name}", "2027-11-01", "listopad"},
		{"{month_name}", "2026-12-31", "grudzień"},
		{"{month_name_en}", "2026-12-31", "December"},
		{"{month_name_en}", "2027-02-01", "February"},
		{"no tokens", "2026-10-31", "no tokens"},
		{"", "2026-10-31", ""},
		{"{year}{year}", "2026-10-31", "20262026"},
	}
	for _, tc := range tests {
		got, err := RenderTemplate(tc.tpl, d(t, tc.sale))
		if err != nil || got != tc.want {
			t.Errorf("%q: got %q, %v; want %q", tc.tpl, got, err, tc.want)
		}
	}
	months := []string{"styczeń", "luty", "marzec", "kwiecień", "maj", "czerwiec", "lipiec", "sierpień", "wrzesień", "październik", "listopad", "grudzień"}
	english := []string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}
	for m := 1; m <= 12; m++ {
		sale := time.Date(2027, time.Month(m), 10, 0, 0, 0, 0, Warsaw())
		if got, _ := RenderTemplate("{month_name}", sale); got != months[m-1] {
			t.Errorf("month %d pl: %q", m, got)
		}
		if got, _ := RenderTemplate("{month_name_en}", sale); got != english[m-1] {
			t.Errorf("month %d en: %q", m, got)
		}
	}
}

func TestRenderTemplateErrors(t *testing.T) {
	sale := d(t, "2026-10-31")
	for _, tpl := range []string{"{month}", "{MONTH_NAME}", "{}", "{ year }", "abc {year", "abc }", "{{year}}", "{seq}"} {
		if _, err := RenderTemplate(tpl, sale); err == nil {
			t.Errorf("%q accepted", tpl)
		}
	}
	if _, err := RenderTemplate("x", time.Time{}); err == nil {
		t.Error("zero sale accepted")
	}
	if _, err := RenderTemplate("{foo}", sale); err == nil || !strings.Contains(err.Error(), "{foo}") {
		t.Errorf("error does not name the token: %v", err)
	}
}

func TestRenderTemplateLimit(t *testing.T) {
	sale := d(t, "2026-10-31")
	ok := strings.Repeat("ż", 512)
	if got, err := RenderTemplate(ok, sale); err != nil || got != ok {
		t.Errorf("512 runes: %v", err)
	}
	if _, err := RenderTemplate(ok+"a", sale); err == nil {
		t.Error("513 runes accepted")
	}
	// The limit applies to the rendered text, not the template.
	if _, err := RenderTemplate(strings.Repeat("ż", 510)+"{year}", sale); err == nil {
		t.Error("rendered 514 runes accepted")
	}
}

func rate(t *testing.T, code string) money.Rate {
	t.Helper()
	r, err := money.ParseRate(code)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func amt(t *testing.T, s string) *money.Amount {
	t.Helper()
	a, err := money.ParseAmount(s)
	if err != nil {
		t.Fatal(err)
	}
	return &a
}

func qty(t *testing.T, s string) *money.Quantity {
	t.Helper()
	q, err := money.ParseQuantity(s)
	if err != nil {
		t.Fatal(err)
	}
	return &q
}

func TestComputeNet(t *testing.T) {
	c, err := Compute(Request{
		IssueDate: d(t, "2026-10-31"), SaleRule: "last_day_of_month", PaymentDays: 14,
		Template: "Usługi IT {month_name} {year}", Unit: "usł.", Net: amt(t, "18500"), Rate: rate(t, "23"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.IssueDate.Format("2006-01-02") != "2026-10-31" || c.SaleDate.Format("2006-01-02") != "2026-10-31" || c.DueDate.Format("2006-01-02") != "2026-11-14" {
		t.Errorf("dates %v %v %v", c.IssueDate, c.SaleDate, c.DueDate)
	}
	if len(c.Lines) != 1 {
		t.Fatalf("lines %d", len(c.Lines))
	}
	l := c.Lines[0]
	if l.No != 1 || l.Description != "Usługi IT październik 2026" || l.Unit != "usł." || l.Qty != 1000000 || l.UnitPrice != 1850000 || l.Net != 1850000 {
		t.Errorf("line %+v", l)
	}
	if len(c.Summary.Groups) != 1 || c.Summary.Groups[0].VAT != 425500 || c.Summary.Gross != 2275500 {
		t.Errorf("summary %+v", c.Summary)
	}
}

func TestComputeQtyPrice(t *testing.T) {
	c, err := Compute(Request{
		IssueDate: d(t, "2026-12-20"), PaymentDays: 14, Template: "x", Unit: "h",
		Qty: qty(t, "160,5"), UnitPrice: amt(t, "100.10"), Rate: rate(t, "8"),
	})
	if err != nil {
		t.Fatal(err)
	}
	l := c.Lines[0]
	want, _ := money.NetOf(*qty(t, "160.5"), *amt(t, "100.10"))
	if l.Net != want || l.UnitPrice != *amt(t, "100.10") || l.Qty != *qty(t, "160.5") {
		t.Errorf("line %+v want net %d", l, want)
	}
	if c.DueDate.Format("2006-01-02") != "2027-01-03" {
		t.Errorf("due %v", c.DueDate)
	}
}

func TestComputeZwAndRates(t *testing.T) {
	for _, code := range []string{"23", "8", "5", "zw"} {
		c, err := Compute(Request{IssueDate: d(t, "2026-10-01"), Template: "x", Net: amt(t, "100"), Rate: rate(t, code)})
		if err != nil {
			t.Fatalf("%s: %v", code, err)
		}
		if c.Summary.Groups[0].Rate.Code != code {
			t.Errorf("%s: %+v", code, c.Summary)
		}
	}
}

func TestComputeSaleDateFlagAndDesc(t *testing.T) {
	flag := d(t, "2026-09-30")
	c, err := Compute(Request{
		IssueDate: d(t, "2026-10-05"), SaleDateFlag: &flag, SaleRule: "last_day_of_month",
		Template: "tpl", DescFlag: "Prace za {month_name_en} {year}", Net: amt(t, "10"), Rate: rate(t, "23"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.SaleDate.Format("2006-01-02") != "2026-09-30" {
		t.Errorf("sale %v", c.SaleDate)
	}
	if c.Lines[0].Description != "Prace za September 2026" {
		t.Errorf("desc %q", c.Lines[0].Description)
	}
	// An explicit sale date may be after the issue date or in another month.
	flag = d(t, "2026-11-02")
	c, err = Compute(Request{IssueDate: d(t, "2026-10-05"), SaleDateFlag: &flag, Template: "{month_name}", Net: amt(t, "10"), Rate: rate(t, "23")})
	if err != nil || c.SaleDate.Format("2006-01-02") != "2026-11-02" || c.Lines[0].Description != "listopad" {
		t.Errorf("%+v %v", c, err)
	}
}

func TestComputeNormalizesDates(t *testing.T) {
	issue := time.Date(2026, 12, 31, 23, 30, 0, 0, time.UTC) // 1 Jan in Warsaw
	c, err := Compute(Request{IssueDate: issue, PaymentDays: 0, Template: "x", Net: amt(t, "1"), Rate: rate(t, "23")})
	if err != nil {
		t.Fatal(err)
	}
	if fmtD(c.IssueDate) != "2027-01-01 00:00 CET" || !c.DueDate.Equal(c.IssueDate) {
		t.Errorf("issue %s due %s", fmtD(c.IssueDate), fmtD(c.DueDate))
	}
}

func TestComputeErrors(t *testing.T) {
	base := func() Request {
		return Request{IssueDate: d(t, "2026-10-31"), Template: "x", Net: amt(t, "100"), Rate: rate(t, "23")}
	}
	tests := []struct {
		name string
		mut  func(*Request)
		want string
	}{
		{"no issue date", func(r *Request) { r.IssueDate = time.Time{} }, "issue date"},
		{"no rate", func(r *Request) { r.Rate = money.Rate{} }, "rate"},
		{"neither", func(r *Request) { r.Net = nil }, "net amount"},
		{"net and qty", func(r *Request) { r.Qty = qty(t, "2") }, "not both"},
		{"net and price", func(r *Request) { r.UnitPrice = amt(t, "2") }, "not both"},
		{"all three", func(r *Request) { r.Qty = qty(t, "2"); r.UnitPrice = amt(t, "2") }, "not both"},
		{"qty only", func(r *Request) { r.Net = nil; r.Qty = qty(t, "2") }, "together"},
		{"price only", func(r *Request) { r.Net = nil; r.UnitPrice = amt(t, "2") }, "together"},
		{"zero net", func(r *Request) { r.Net = new(money.Amount) }, "positive"},
		{"net rounds to zero", func(r *Request) { r.Net = nil; r.Qty = qty(t, "0.000001"); r.UnitPrice = amt(t, "0.01") }, "net amount"},
		{"bad rule", func(r *Request) { r.SaleRule = "soon" }, "soon"},
		{"negative days", func(r *Request) { r.PaymentDays = -1 }, "negative"},
		{"empty description", func(r *Request) { r.Template = "  " }, "description"},
		{"bad token", func(r *Request) { r.Template = "{nope}" }, "{nope}"},
		{"bad desc flag", func(r *Request) { r.DescFlag = "{nope}" }, "{nope}"},
	}
	for _, tc := range tests {
		r := base()
		tc.mut(&r)
		_, err := Compute(r)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want error containing %q", tc.name, err, tc.want)
		}
	}
}

func TestComputeIsPure(t *testing.T) {
	r := Request{IssueDate: d(t, "2028-02-29"), SaleRule: "last_day_of_previous_month", PaymentDays: 30, Template: "{month_name} {year}", Net: amt(t, "5.55"), Rate: rate(t, "5")}
	a, err := Compute(r)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Compute(r)
	if fmtD(a.SaleDate) != "2028-01-31 00:00 CET" || a.Lines[0].Description != "styczeń 2028" || fmtD(a.DueDate) != "2028-03-30 00:00 CEST" {
		t.Errorf("%s %q %s", fmtD(a.SaleDate), a.Lines[0].Description, fmtD(a.DueDate))
	}
	if a.Lines[0] != b.Lines[0] || a.Summary.Gross != b.Summary.Gross {
		t.Error("not deterministic")
	}
}

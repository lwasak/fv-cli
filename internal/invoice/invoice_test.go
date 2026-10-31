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

// one wraps a single line spec the way the single-line flags do.
func one(l LineSpec) []LineSpec { return []LineSpec{l} }

func TestComputeNet(t *testing.T) {
	c, err := Compute(Request{
		IssueDate: d(t, "2026-10-31"), SaleRule: "last_day_of_month", PaymentDays: 14,
		Lines: one(LineSpec{Template: "Usługi IT {month_name} {year}", Unit: "usł.", Net: amt(t, "18500"), Rate: rate(t, "23")}),
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
		IssueDate: d(t, "2026-12-20"), PaymentDays: 14,
		Lines: one(LineSpec{Template: "x", Unit: "h", Qty: qty(t, "160,5"), UnitPrice: amt(t, "100.10"), Rate: rate(t, "8")}),
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
		c, err := Compute(Request{IssueDate: d(t, "2026-10-01"), Lines: one(LineSpec{Template: "x", Net: amt(t, "100"), Rate: rate(t, code)})})
		if err != nil {
			t.Fatalf("%s: %v", code, err)
		}
		if c.Summary.Groups[0].Rate.Code != code {
			t.Errorf("%s: %+v", code, c.Summary)
		}
	}
}

func TestComputeSaleDateFlag(t *testing.T) {
	flag := d(t, "2026-09-30")
	c, err := Compute(Request{
		IssueDate: d(t, "2026-10-05"), SaleDateFlag: &flag, SaleRule: "last_day_of_month",
		Lines: one(LineSpec{Template: "Prace za {month_name_en} {year}", Net: amt(t, "10"), Rate: rate(t, "23")}),
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
	c, err = Compute(Request{IssueDate: d(t, "2026-10-05"), SaleDateFlag: &flag, Lines: one(LineSpec{Template: "{month_name}", Net: amt(t, "10"), Rate: rate(t, "23")})})
	if err != nil || c.SaleDate.Format("2006-01-02") != "2026-11-02" || c.Lines[0].Description != "listopad" {
		t.Errorf("%+v %v", c, err)
	}
}

func TestComputeNormalizesDates(t *testing.T) {
	issue := time.Date(2026, 12, 31, 23, 30, 0, 0, time.UTC) // 1 Jan in Warsaw
	c, err := Compute(Request{IssueDate: issue, PaymentDays: 0, Lines: one(LineSpec{Template: "x", Net: amt(t, "1"), Rate: rate(t, "23")})})
	if err != nil {
		t.Fatal(err)
	}
	if fmtD(c.IssueDate) != "2027-01-01 00:00 CET" || !c.DueDate.Equal(c.IssueDate) {
		t.Errorf("issue %s due %s", fmtD(c.IssueDate), fmtD(c.DueDate))
	}
}

func TestComputeErrors(t *testing.T) {
	base := func() Request {
		return Request{IssueDate: d(t, "2026-10-31"), Lines: one(LineSpec{Template: "x", Net: amt(t, "100"), Rate: rate(t, "23")})}
	}
	tests := []struct {
		name string
		mut  func(*Request)
		want string
	}{
		{"no issue date", func(r *Request) { r.IssueDate = time.Time{} }, "issue date"},
		{"no lines", func(r *Request) { r.Lines = nil }, "no lines"},
		{"no rate", func(r *Request) { r.Lines[0].Rate = money.Rate{} }, "rate"},
		{"neither", func(r *Request) { r.Lines[0].Net = nil }, "net amount"},
		{"net and qty", func(r *Request) { r.Lines[0].Qty = qty(t, "2") }, "not both"},
		{"net and price", func(r *Request) { r.Lines[0].UnitPrice = amt(t, "2") }, "not both"},
		{"all three", func(r *Request) { r.Lines[0].Qty = qty(t, "2"); r.Lines[0].UnitPrice = amt(t, "2") }, "not both"},
		{"qty only", func(r *Request) { r.Lines[0].Net = nil; r.Lines[0].Qty = qty(t, "2") }, "together"},
		{"zero net", func(r *Request) { r.Lines[0].Net = new(money.Amount) }, "positive"},
		{"net rounds to zero", func(r *Request) {
			r.Lines[0].Net = nil
			r.Lines[0].Qty = qty(t, "0.000001")
			r.Lines[0].UnitPrice = amt(t, "0.01")
		}, "net amount"},
		{"bad rule", func(r *Request) { r.SaleRule = "soon" }, "soon"},
		{"negative days", func(r *Request) { r.PaymentDays = -1 }, "negative"},
		{"empty description", func(r *Request) { r.Lines[0].Template = "  " }, "description"},
		{"bad token", func(r *Request) { r.Lines[0].Template = "{nope}" }, "{nope}"},
	}
	for _, tc := range tests {
		r := base()
		tc.mut(&r)
		_, err := Compute(r)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want error containing %q", tc.name, err, tc.want)
		}
		if err != nil && strings.Contains(err.Error(), "line 1") {
			t.Errorf("%s: a line without Source must not get a prefix: %v", tc.name, err)
		}
	}
}

func TestComputeIsPure(t *testing.T) {
	r := Request{IssueDate: d(t, "2028-02-29"), SaleRule: "last_day_of_previous_month", PaymentDays: 30,
		Lines: one(LineSpec{Template: "{month_name} {year}", Net: amt(t, "5.55"), Rate: rate(t, "5")})}
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

func TestComputeUnitPriceWithoutQtyIsOne(t *testing.T) {
	c, err := Compute(Request{IssueDate: d(t, "2026-10-01"),
		Lines: one(LineSpec{Template: "x", UnitPrice: amt(t, "19,99"), Rate: rate(t, "23")})})
	if err != nil {
		t.Fatal(err)
	}
	if l := c.Lines[0]; l.Qty != oneUnit || l.Net != *amt(t, "19,99") || l.UnitPrice != *amt(t, "19,99") {
		t.Errorf("line %+v", l)
	}
}

func TestComputeMultiLine(t *testing.T) {
	c, err := Compute(Request{
		IssueDate: d(t, "2026-10-31"), SaleRule: "last_day_of_month",
		Lines: []LineSpec{
			{Source: `--item "dev=160"`, Item: "dev", Template: "Dev {month_name} {year}", Unit: "h", Qty: qty(t, "160"), UnitPrice: amt(t, "120"), Rate: rate(t, "23"), Index: "DEV-01"},
			{Source: `--line "desc=Mix"`, Template: "Mix {month_name_en}", Unit: "szt.", Qty: qty(t, "2,5"), UnitPrice: amt(t, "33,33"), Rate: rate(t, "23"), GTIN: "5901234123457"},
			{Source: `--line "desc=Book"`, Template: "Book", Net: amt(t, "100"), Rate: rate(t, "8"), PKWiU: "58.11"},
			{Source: `--line "desc=Training"`, Template: "Training", Net: amt(t, "50"), Rate: rate(t, "zw")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Lines) != 4 {
		t.Fatalf("lines %d", len(c.Lines))
	}
	for i, l := range c.Lines {
		if l.No != i+1 {
			t.Errorf("line %d has No %d", i, l.No)
		}
	}
	if c.Lines[0].Item != "dev" || c.Lines[0].Index != "DEV-01" || c.Lines[1].GTIN != "5901234123457" || c.Lines[2].PKWiU != "58.11" {
		t.Errorf("identifiers not carried: %+v", c.Lines)
	}
	if c.Lines[0].Description != "Dev październik 2026" || c.Lines[1].Description != "Mix October" {
		t.Errorf("tokens: %q %q", c.Lines[0].Description, c.Lines[1].Description)
	}
	// 160 x 120,00 = 19 200,00 and 2,5 x 33,33 = 83,325 -> 83,33; net 19 283,33.
	if c.Lines[1].Net != *amt(t, "83,33") {
		t.Errorf("line 2 net %d", c.Lines[1].Net)
	}
	want := map[string][3]money.Amount{
		"23": {*amt(t, "19283,33"), *amt(t, "4435,17"), *amt(t, "23718,50")},
		"8":  {*amt(t, "100"), *amt(t, "8"), *amt(t, "108")},
		"zw": {*amt(t, "50"), 0, *amt(t, "50")},
	}
	if len(c.Summary.Groups) != len(want) {
		t.Fatalf("groups %+v", c.Summary.Groups)
	}
	for _, g := range c.Summary.Groups {
		w := want[g.Rate.Code]
		if g.Net != w[0] || g.VAT != w[1] || g.Gross != w[2] {
			t.Errorf("group %s = %+v, want %v", g.Rate.Code, g, w)
		}
	}
	if c.Summary.Gross != *amt(t, "23876,50") {
		t.Errorf("gross %d", c.Summary.Gross)
	}
}

// Three lines of 0,02 at 23% give VAT 0,01 on the group; rounding each line would give 0,00.
func TestComputeVATIsRoundedPerRateGroup(t *testing.T) {
	var lines []LineSpec
	for i := 0; i < 3; i++ {
		lines = append(lines, LineSpec{Template: "x", Net: amt(t, "0,02"), Rate: rate(t, "23")})
	}
	c, err := Compute(Request{IssueDate: d(t, "2026-10-01"), Lines: lines})
	if err != nil {
		t.Fatal(err)
	}
	g := c.Summary.Groups[0]
	if g.Net != 6 || g.VAT != 1 || g.Gross != 7 {
		t.Errorf("group %+v, want net 6 VAT 1 gross 7", g)
	}
}

func TestComputeLineErrorsNameTheLine(t *testing.T) {
	ok := LineSpec{Source: `--item "dev"`, Template: "x", Net: amt(t, "1"), Rate: rate(t, "23")}
	tests := []struct {
		name string
		bad  LineSpec
		want string
	}{
		{"no amount", LineSpec{Source: `--line "desc=Hosting"`, Template: "x", Rate: rate(t, "23")}, `line 2 (--line "desc=Hosting"): give a net amount`},
		{"net and qty", LineSpec{Source: "S", Template: "x", Net: amt(t, "1"), Qty: qty(t, "2"), Rate: rate(t, "23")}, "line 2 (S): give either net"},
		{"no rate", LineSpec{Source: "S", Template: "x", Net: amt(t, "1")}, "line 2 (S): VAT rate is required"},
		{"bad token", LineSpec{Source: "S", Template: "{nope}", Net: amt(t, "1"), Rate: rate(t, "23")}, "line 2 (S): "},
		{"empty description", LineSpec{Source: "S", Template: " ", Net: amt(t, "1"), Rate: rate(t, "23")}, "line 2 (S): line description is empty"},
	}
	for _, tc := range tests {
		_, err := Compute(Request{IssueDate: d(t, "2026-10-01"), Lines: []LineSpec{ok, tc.bad}})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.name, err, tc.want)
		}
	}
}

func TestComputeLineLimit(t *testing.T) {
	spec := LineSpec{Template: "x", Net: amt(t, "1"), Rate: rate(t, "23")}
	lines := make([]LineSpec, MaxLines)
	for i := range lines {
		lines[i] = spec
	}
	if c, err := Compute(Request{IssueDate: d(t, "2026-10-01"), Lines: lines}); err != nil || len(c.Lines) != MaxLines {
		t.Fatalf("%d lines: %v", MaxLines, err)
	}
	_, err := Compute(Request{IssueDate: d(t, "2026-10-01"), Lines: append(lines, spec)})
	if err == nil || !strings.Contains(err.Error(), "10000") {
		t.Errorf("10001 lines: %v", err)
	}
}

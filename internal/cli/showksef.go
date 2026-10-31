package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"text/tabwriter"
	"unicode"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/fa3"
	"github.com/lwasak/fv-cli/internal/invoice"
	"github.com/lwasak/fv-cli/internal/ksef"
	"github.com/lwasak/fv-cli/internal/ksefcache"
	"github.com/lwasak/fv-cli/internal/money"
)

// showFromKSeF shows the invoice with this KSeF number from the cache or, when it has not been
// fetched yet, after one download (which is then cached for good). It never queries the
// metadata, which costs the scarce 20-an-hour budget.
func (s *session) showFromKSeF(cmd *cobra.Command, rc *runContext, cache *ksefcache.Cache, ksefNo string, asXML bool) error {
	b, err := cache.XML(ksefNo)
	fromCache := err == nil
	switch {
	case err == nil:
	case errors.Is(err, ksefcache.ErrNotCached):
		if b, err = s.downloadInvoice(cmd, rc, cache, ksefNo); err != nil {
			return err
		}
	default:
		return fmt.Errorf("%w; fv show will not use it. Delete it to download the invoice again", err)
	}
	out := cmd.OutOrStdout()
	if asXML {
		_, err := out.Write(b)
		return err
	}
	e, _, _ := cache.ByKSeFNumber(ksefNo)
	o := previewOpts{Color: colorOK(out)}
	if fa, why := faSupported(e); !fa {
		renderKSeFSummary(out, rc.Env, e, ksefNo, why, o)
		return nil
	}
	doc, perr := fa3.Parse(b)
	if perr != nil {
		renderKSeFSummary(out, rc.Env, e, ksefNo, "the XML could not be read ("+perr.Error()+")", o)
		return nil
	}
	renderKSeFInvoice(out, doc, e, ksefNo, rc.Config.Seller.NIP, rc.Env, fromCache, o)
	return nil
}

// faSupported reports whether the metadata says the invoice is an FA(2) or FA(3) one; without
// metadata the XML decides.
func faSupported(e ksefcache.Entry) (bool, string) {
	switch e.FormCode {
	case "", "FA (2)", "FA (3)":
		return true, ""
	}
	return false, e.FormCode
}

// downloadInvoice fetches the XML of ksefNo from KSeF (login, one GET) and caches it.
func (s *session) downloadInvoice(cmd *cobra.Command, rc *runContext, cache *ksefcache.Cache, ksefNo string) ([]byte, error) {
	l, closeLog := s.logger(rc.Paths)
	defer closeLog()
	ctx, stop := signal.NotifyContext(commandContext(cmd), os.Interrupt)
	defer stop()
	client, sess, err := s.ksefLogin(ctx, rc, l)
	if err != nil {
		return nil, err
	}
	b, hash, err := client.DownloadInvoice(ctx, sess, ksefNo)
	switch {
	case err == nil:
	case ksef.IsNotYetAvailable(err):
		return nil, errors.New("KSeF has not made this invoice downloadable yet; retry in a minute")
	case ksef.IsNoSuchInvoice(err):
		return nil, fmt.Errorf("KSeF has no invoice %s in the %s environment (or your NIP may not read it)", ksefNo, rc.Env)
	case errors.Is(err, ksef.ErrDownloadHash):
		return nil, fmt.Errorf("%w; nothing was cached", err)
	default:
		return nil, err
	}
	if err := cache.PutXML(ctx, ksefNo, b, hash); errors.Is(err, ksefcache.ErrHashMismatch) {
		return nil, fmt.Errorf("%w; nothing was cached and the invoice is not shown", err)
	} else if err != nil {
		_, _ = fmt.Fprintf(s.deps.Stderr, "warning: the invoice could not be cached, so the next show downloads it again: %v\n", err)
	}
	return b, nil
}

// ambiguousNumber is the error for an invoice number that two or more suppliers used.
func ambiguousNumber(number string, es []ksefcache.Entry) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%q matches %d invoices in the KSeF rows of your last fv list; give the KSeF number:\n", clean(number), len(es))
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  ISSUED\tSELLER\tNIP\tKSEF-NUMBER")
	for _, e := range es {
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", e.IssueDate, orDash(cut(clean(e.SellerName), nameWidth)), orDash(clean(e.SellerNIP)), e.KSeFNumber)
	}
	_ = tw.Flush()
	return errors.New(strings.TrimRight(b.String(), "\n"))
}

func notInCache(number string) error {
	return fmt.Errorf("%q is not in the archive or in the KSeF rows of your last fv list; run `fv list --from YYYY-MM-DD --to YYYY-MM-DD` covering its issue date, or pass the KSeF number", clean(number))
}

// --- rendering ---

func envBanner(env config.Env, o previewOpts) string {
	banner := "ENVIRONMENT: " + strings.ToUpper(string(env))
	if o.Color && env == config.EnvProd {
		banner = "\x1b[1;31m" + banner + "\x1b[0m"
	}
	return banner
}

// renderKSeFSummary prints what the metadata knows of an invoice whose document fv does not
// render, with the reason and the hint to --xml.
func renderKSeFSummary(w io.Writer, env config.Env, e ksefcache.Entry, ksefNo, reason string, o previewOpts) {
	p := func(format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }
	p("%s\n\n", envBanner(env, o))
	p("Invoice:   %s   [KSeF]\n", orDash(clean(e.InvoiceNumber)))
	p("Issued:    %s\n", orDash(e.IssueDate))
	p("Seller:    %s  NIP %s\n", orDash(clean(e.SellerName)), orDash(clean(e.SellerNIP)))
	p("Buyer:     %s  %s %s\n", orDash(clean(e.BuyerName)), orDash(clean(e.BuyerIDType)), orDash(clean(e.BuyerID)))
	if g, err := money.ParseXML(e.Gross.String()); err == nil {
		p("Total:     %s %s\n", money.FormatPL(g), orDash(clean(e.Currency)))
	}
	p("Kind:      %s\n", orDash(clean(e.InvoiceType)))
	p("KSeF no.:  %s\n", ksefNo)
	p("\nreadable view not supported for %s; use --xml\n", orDash(clean(reason)))
}

// kRate labels a P_12 value: numeric rates get a percent sign, the others stay as written.
func kRate(code string) string {
	if code == "" {
		return "-"
	}
	for _, r := range code {
		if !unicode.IsDigit(r) {
			return clean(code)
		}
	}
	return code + "%"
}

// fmtDec shows an XML decimal the Polish way: comma, at least minDec decimals, trailing zeros
// beyond them dropped, a space between thousands from five digits up.
func fmtDec(s string, minDec int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "-"
	}
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	whole, frac, _ := strings.Cut(s, ".")
	frac = strings.TrimRight(frac, "0")
	for len(frac) < minDec {
		frac += "0"
	}
	if len(whole) >= 5 {
		var out []byte
		for i := 0; i < len(whole); i++ {
			if i > 0 && (len(whole)-i)%3 == 0 {
				out = append(out, ' ')
			}
			out = append(out, whole[i])
		}
		whole = string(out)
	}
	if frac == "" {
		return sign + whole
	}
	return sign + whole + "," + frac
}

func docAddress(p fa3.DocParty) string {
	var parts []string
	for _, x := range []string{p.AddressL1, p.AddressL2} {
		if x = clean(x); x != "" {
			parts = append(parts, x)
		}
	}
	if c := clean(p.Country); c != "" && c != "PL" && len(parts) > 0 {
		parts = append(parts, c)
	}
	return strings.Join(parts, ", ")
}

func renderDocParty(w io.Writer, label string, p fa3.DocParty) {
	line := func(v string) {
		_, _ = fmt.Fprintf(w, "%-10s %s\n", label, v)
		label = ""
	}
	line(orDash(clean(p.Name)))
	if a := docAddress(p); a != "" {
		line(a)
	}
	switch {
	case p.NIP != "":
		line("NIP " + clean(p.ID()))
	case p.VatUE != "":
		line("VAT id " + clean(p.VatUE))
	case p.OtherID != "":
		line("ID " + clean(p.OtherID))
	case p.NoID:
		line("no tax identifier")
	}
	if c := strings.TrimSpace(clean(p.Email) + " " + clean(p.Phone)); c != "" {
		line(c)
	}
}

// renderKSeFInvoice prints an FA(2) or FA(3) invoice of any issuer, as KSeF stores it. It
// keeps the banner and party layout of renderInvoice but reads the document itself, because
// unit prices have up to 8 decimals, lines can be gross, and rates can be np, oo or 0 KR.
func renderKSeFInvoice(w io.Writer, d *fa3.Document, e ksefcache.Entry, ksefNo, ownNIP string, env config.Env, fromCache bool, o previewOpts) {
	p := func(format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }
	dir := "purchase"
	if d.Seller.NIP != "" && d.Seller.NIP == ownNIP {
		dir = "sale"
	}
	p("%s\n\n", envBanner(env, o))
	p("Invoice:   %s   [%s, KSeF]\n", orDash(clean(d.Number)), dir)
	p("Kind:      %s\n", orDash(clean(d.Kind)))
	p("Issued:    %s\n", orDash(clean(d.IssueDate)))
	switch {
	case d.PeriodFrom != "" || d.PeriodTo != "":
		p("Period:    %s to %s\n", orDash(clean(d.PeriodFrom)), orDash(clean(d.PeriodTo)))
	case d.SaleDate != "":
		p("Sale date: %s\n", clean(d.SaleDate))
	}
	if d.Place != "" {
		p("Place:     %s\n", clean(d.Place))
	}
	cur := orDash(clean(d.Currency))
	if d.ExchangeRate != "" {
		p("Currency:  %s, rate %s\n", cur, clean(d.ExchangeRate))
	} else {
		p("Currency:  %s\n", cur)
	}

	f := d.Flags
	if f.SplitPayment {
		p("\n*** SPLIT PAYMENT: pay with the split payment mechanism (P_18A) ***\n")
	}
	var marks []string
	if f.ReverseCharge {
		marks = append(marks, "reverse charge (P_18)")
	}
	if f.CashAccounting {
		marks = append(marks, "cash accounting method (P_16)")
	}
	if f.SelfInvoicing {
		marks = append(marks, "self-invoicing (P_17)")
	}
	if f.Margin {
		marks = append(marks, "margin scheme")
	}
	if f.NewTransportMeans {
		marks = append(marks, "supply of new means of transport")
	}
	if f.TripartiteSimple {
		marks = append(marks, "simplified tripartite transaction (P_23)")
	}
	if len(marks) > 0 {
		p("Marked:    %s\n", strings.Join(marks, "; "))
	}
	if f.Exempt {
		basis := "no basis given"
		if len(f.ExemptionBasis) > 0 {
			basis = clean(strings.Join(f.ExemptionBasis, "; "))
		}
		p("Exempt:    %s\n", basis)
	}
	for _, c := range d.Corrected {
		ref := orDash(clean(c.Number))
		if c.IssueDate != "" {
			ref += " of " + clean(c.IssueDate)
		}
		if c.KSeFNumber != "" {
			ref += ", KSeF " + clean(c.KSeFNumber)
		} else if c.NoKSeF {
			ref += ", not in KSeF"
		}
		p("Corrects:  %s\n", ref)
	}
	if d.CorrectionReason != "" {
		p("Reason:    %s\n", clean(d.CorrectionReason))
	}
	p("\n")
	renderDocParty(w, "Seller:", d.Seller)
	p("\n")
	renderDocParty(w, "Buyer:", d.Buyer)
	for _, x := range d.Others {
		p("\n")
		renderDocParty(w, roleLabel(x.Role), x)
	}
	p("\n")

	gross := false
	if len(d.Lines) > 0 {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
		_, _ = fmt.Fprintln(tw, "No\tDescription\tQty\tUnit\tPrice\tValue\tRate\t")
		for _, l := range d.Lines {
			desc := orDash(clean(l.Description))
			if l.Before {
				desc = "[before correction] " + desc
			}
			price := fmtDec(l.UnitPrice, 2)
			if l.UnitPriceGross {
				price += "*"
				gross = true
			}
			value := "-"
			if l.HasValue {
				value = money.FormatPL(l.Value)
				if l.ValueGross {
					value += "*"
					gross = true
				}
			}
			_, _ = fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t\n", l.No, desc, fmtDec(l.Qty, 0), orDash(clean(l.Unit)), price, value, kRate(l.Rate))
		}
		_ = tw.Flush()
		if gross {
			p("* gross amount, as the issuer wrote it\n")
		}
		p("\n")
	} else if d.Kind == "ZAL" || d.OrderLines > 0 {
		p("Advance invoice; order lines (ZamowienieWiersz) not shown (%d in the document).\n\n", d.OrderLines)
	} else {
		p("The document has no invoice lines.\n\n")
	}

	if len(d.Totals) > 0 {
		inPLN := false
		for _, t := range d.Totals {
			inPLN = inPLN || t.HasVATPLN
		}
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
		header := "Rate\tNet\tVAT\tGross\t"
		if inPLN {
			header = "Rate\tNet\tVAT\tGross\tVAT PLN\t"
		}
		_, _ = fmt.Fprintln(tw, header)
		for _, t := range d.Totals {
			net, vat, gr := money.FormatPL(t.Net), "-", money.FormatPL(t.Net)
			if t.HasVAT {
				vat, gr = money.FormatPL(t.VAT), money.FormatPL(t.Net+t.VAT)
			}
			if t.Key == "11" {
				// the margin procedure (P_13_11) states the gross sale value, not a net amount
				net, vat = "-", "-"
			}
			row := fmt.Sprintf("%s\t%s\t%s\t%s\t", clean(t.Label), net, vat, gr)
			if inPLN {
				pln := "-"
				if t.HasVATPLN {
					pln = money.FormatPL(t.VATPLN)
				}
				row += pln + "\t"
			}
			_, _ = fmt.Fprintln(tw, row)
		}
		_ = tw.Flush()
	}
	if d.HasGross {
		p("Total:     %s %s\n", money.FormatPL(d.Gross), cur)
	} else {
		p("Total:     ? %s\n", cur)
	}
	p("\n")

	pay := d.Payment
	if pay.Method != "" {
		p("Payment:   %s\n", clean(pay.Method))
	}
	for _, due := range pay.Due {
		switch {
		case due.Date != "" && due.Description != "":
			p("Due:       %s (%s)\n", clean(due.Date), clean(due.Description))
		case due.Date != "":
			p("Due:       %s\n", clean(due.Date))
		case due.Description != "":
			p("Due:       %s\n", clean(due.Description))
		}
	}
	if pay.Paid {
		p("Paid:      %s\n", orDash(clean(pay.PaidDate)))
	}
	for _, a := range pay.Accounts {
		line := "Account:   " + groupIBAN(accountWithCountry(clean(a.Number)))
		var extra []string
		if a.Factor {
			extra = append(extra, "FACTOR account: pay here, not to the supplier")
		}
		if a.Bank != "" {
			extra = append(extra, clean(a.Bank))
		}
		if a.SWIFT != "" {
			extra = append(extra, "SWIFT "+clean(a.SWIFT))
		}
		if a.Description != "" {
			extra = append(extra, clean(a.Description))
		}
		if len(extra) > 0 {
			line += "  (" + strings.Join(extra, ", ") + ")"
		}
		p("%s\n", line)
	}
	for _, n := range d.Notes {
		p("Note:      %s: %s\n", clean(n.Key), clean(n.Value))
	}

	p("\n")
	src := "KSeF, downloaded"
	if !e.XMLFetchedAt.IsZero() {
		src += " " + e.XMLFetchedAt.In(invoice.Warsaw()).Format("2006-01-02 15:04")
	} else {
		src += " earlier"
	}
	if fromCache {
		src += " (from the local cache"
	} else {
		src += " (just now"
	}
	p("Source:    %s, SHA-256 checked)\n", src)
	p("KSeF no.:  %s\n", ksefNo)
	for _, msg := range d.Warnings {
		p("warning: %s\n", clean(msg))
	}
}

// roleLabel is the label of a Podmiot3 block: the cleaned role with its first letter, whole
// rune, in capitals.
func roleLabel(role string) string {
	role = clean(role)
	if role == "" {
		return "Other:"
	}
	r, n := utf8.DecodeRuneInString(role)
	return string(unicode.ToUpper(r)) + role[n:] + ":"
}

// accountWithCountry puts PL in front of a bare 26-digit Polish account number (FA(3) writes
// it without), so it reads as the IBAN it is; anything else stays as written.
func accountWithCountry(n string) string {
	if len(n) == 26 && strings.Trim(n, "0123456789") == "" {
		return "PL" + n
	}
	return n
}

package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"golang.org/x/term"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/invoice"
	"github.com/lwasak/fv-cli/internal/money"
	"github.com/lwasak/fv-cli/internal/store"
)

// previewOpts tunes renderInvoice.
type previewOpts struct {
	// Color makes the PROD banner red. It is set only when stdout is a terminal.
	Color bool
	// Detail adds the archive facts fv show prints: XML size and hash, KSeF references, UPO.
	Detail bool
}

// colorOK reports whether w is a terminal that should get ANSI colour.
func colorOK(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok || os.Getenv("NO_COLOR") != "" {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// groupIBAN writes an account number in groups of four, with a Polish national number
// (26 digits) shown the way Polish banks print it: two check digits, then groups of four.
func groupIBAN(a string) string {
	a = strings.ToUpper(strings.ReplaceAll(a, " ", ""))
	if a == "" {
		return ""
	}
	var parts []string
	for len(a) > 4 {
		parts = append(parts, a[:4])
		a = a[4:]
	}
	return strings.Join(append(parts, a), " ")
}

func rateLabel(code string) string {
	if code == "zw" {
		return "zw"
	}
	return code + "%"
}

// renderInvoice prints the readable invoice from its meta.json snapshot. It is the shared
// view of fv issue (the preview) and fv show.
func renderInvoice(w io.Writer, m *store.Meta, o previewOpts) {
	banner := "ENVIRONMENT: " + strings.ToUpper(string(m.Env))
	if o.Color && m.Env == config.EnvProd {
		banner = "\x1b[1;31m" + banner + "\x1b[0m"
	}
	p := func(format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }
	p("%s\n\n", banner)
	p("Invoice:   %s   [%s]\n", m.Number, m.State)
	p("Issued:    %s\n", m.IssueDate)
	p("Sale date: %s\n", m.SaleDate)
	p("Due date:  %s\n", m.DueDate)
	if m.Exchange != nil {
		p("Currency:  %s, rate %s PLN (NBP %s, %s)\n", orDash(m.Currency), m.Exchange.Mid, m.Exchange.Table, m.Exchange.EffectiveDate)
	} else {
		p("Currency:  %s\n", orDash(m.Currency))
	}
	p("\n")
	renderPartySnap(w, "Seller", m.Seller)
	p("\n")
	renderPartySnap(w, "Buyer", m.Buyer)
	p("\n")

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	hasIndex := false
	for _, l := range m.Lines {
		hasIndex = hasIndex || l.Index != ""
	}
	if hasIndex {
		_, _ = fmt.Fprintln(tw, "No\tIndex\tDescription\tQty\tUnit\tPrice\tNet\tRate\t")
	} else {
		_, _ = fmt.Fprintln(tw, "No\tDescription\tQty\tUnit\tPrice\tNet\tRate\t")
	}
	for _, l := range m.Lines {
		cols := fmt.Sprintf("%d\t", l.No)
		if hasIndex {
			cols += orDash(l.Index) + "\t"
		}
		_, _ = fmt.Fprintf(tw, "%s%s\t%s\t%s\t%s\t%s\t%s\t\n", cols, l.Description, trimQty(l.Quantity),
			l.Unit, money.FormatPL(l.UnitPrice), money.FormatPL(l.Net), rateLabel(l.Rate))
	}
	_ = tw.Flush()
	p("\n")

	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	foreign := m.Exchange != nil
	header := "Rate\tNet\tVAT\tGross\t"
	if foreign {
		header = "Rate\tNet\tVAT\tGross\tVAT PLN\t"
	}
	_, _ = fmt.Fprintln(tw, header)
	for _, t := range m.Totals {
		vat := money.FormatPL(t.VAT)
		if t.Rate == "zw" {
			vat = "-"
		}
		row := fmt.Sprintf("%s\t%s\t%s\t%s\t", rateLabel(t.Rate), money.FormatPL(t.Net), vat, money.FormatPL(t.Gross))
		if foreign {
			pln := "-"
			if t.VATPLN != nil {
				pln = money.FormatPL(*t.VATPLN)
			}
			row += pln + "\t"
		}
		_, _ = fmt.Fprintln(tw, row)
	}
	_ = tw.Flush()
	p("Total:     %s %s\n", money.FormatPL(m.Gross), orDash(m.Currency))
	p("\n")

	acct := "Account:   " + groupIBAN(m.BankAccount)
	switch {
	case m.BankOnWhiteList == nil:
		acct += "  (on White List: unknown)"
	case *m.BankOnWhiteList:
		acct += "  (on White List: yes)"
	default:
		acct += "  (on White List: NO)"
	}
	p("%s\n", acct)
	if m.BankName != "" || m.BankSWIFT != "" {
		var bank []string
		if m.BankName != "" {
			bank = append(bank, m.BankName)
		}
		if m.BankSWIFT != "" {
			bank = append(bank, "SWIFT "+m.BankSWIFT)
		}
		p("Bank:      %s\n", strings.Join(bank, ", "))
	}
	p("Payment:   transfer (FormaPlatnosci 6)\n")

	if o.Detail {
		p("\nState:     %s\n", m.State)
		if m.XMLSize > 0 {
			p("XML:       %d bytes, SHA-256 %s\n", m.XMLSize, m.XMLSHA256)
		}
		if m.KSeF != nil && m.KSeF.KSeFNumber != "" {
			p("KSeF no.:  %s\n", m.KSeF.KSeFNumber)
		} else {
			p("KSeF no.:  -\n")
		}
		if m.UPOSHA256 != "" {
			p("UPO:       stored\n")
		} else {
			p("UPO:       -\n")
		}
		if m.KSeF != nil && m.KSeF.Duplicate {
			if m.KSeF.OriginalKSeFNumber != "" {
				p("KSeF duplicate: this number is already used in KSeF by %s\n", m.KSeF.OriginalKSeFNumber)
			} else {
				p("KSeF duplicate: this number is already used by an invoice in KSeF\n")
			}
		}
		if m.KSeF != nil && m.KSeF.StatusDescription != "" {
			p("KSeF status: %d %s\n", m.KSeF.StatusCode, m.KSeF.StatusDescription)
			for _, d := range m.KSeF.Details {
				p("  %s\n", d)
			}
		}
	}
	if len(m.Warnings) > 0 {
		p("\n")
		for _, msg := range m.Warnings {
			p("warning: %s\n", msg)
		}
	}
}

func renderPartySnap(w io.Writer, role string, s store.PartySnapshot) {
	label := role + ":"
	line := func(v string) {
		_, _ = fmt.Fprintf(w, "%-10s %s\n", label, v)
		label = ""
	}
	line(orDash(s.Name))
	if s.Address != "" {
		line(s.Address)
	}
	if s.NIP != "" {
		line("NIP " + s.NIP)
	} else if s.VatID != "" {
		line("VAT id " + s.VatID)
	}
	if s.StatusVat != "" || s.RequestID != "" || s.ConsultationNumber != "" {
		var b strings.Builder
		if s.StatusVat != "" {
			b.WriteString("VAT: " + s.StatusVat)
		}
		var meta []string
		if !s.CheckedAt.IsZero() {
			meta = append(meta, "checked "+s.CheckedAt.In(invoice.Warsaw()).Format("2006-01-02 15:04"))
		}
		if s.RequestID != "" {
			meta = append(meta, "White List requestId "+s.RequestID)
		}
		if s.ConsultationNumber != "" {
			meta = append(meta, "VIES consultation "+s.ConsultationNumber)
		}
		if len(meta) > 0 {
			if b.Len() > 0 {
				b.WriteString(" ")
			}
			b.WriteString("(" + strings.Join(meta, ", ") + ")")
		}
		line(b.String())
	}
}

// trimQty shows an XML decimal quantity with the Polish decimal comma.
func trimQty(q string) string {
	if q == "" {
		return "-"
	}
	return strings.ReplaceAll(q, ".", ",")
}

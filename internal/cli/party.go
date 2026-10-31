package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/exitcode"
	"github.com/lwasak/fv-cli/internal/fsafe"
	"github.com/lwasak/fv-cli/internal/registry"
	"github.com/lwasak/fv-cli/internal/validate"
)

// newPartyCmd builds the fv party command group (add, show, refresh).
func newPartyCmd(s *session) *cobra.Command {
	party := &cobra.Command{
		Use:   "party",
		Short: "Add, show and refresh invoice parties (buyers)",
		Long: "Parties are the buyers you invoice. You give a tax number only; fv looks the name, " +
			"address, VAT status and bank accounts up in the MF VAT White List (Polish NIP) or in " +
			"EU VIES (EU VAT id) and caches the answer in parties.json in the data directory. " +
			"Values in [parties.<alias>] in the config (name, address) win over the registry.",
		Example: "  fv party add acme --nip 5252248481\n  fv party show acme\n  fv party refresh --all",
	}
	party.AddCommand(s.newPartyAddCmd(), s.newPartyShowCmd(), s.newPartyRefreshCmd())
	return party
}

func (s *session) newPartyAddCmd() *cobra.Command {
	var nip, vatID, name, address string
	cmd := &cobra.Command{
		Use:   "add <alias> (--nip NIP | --vat-id ID)",
		Short: "Look a party up by tax number and add it to the config",
		Long: "Validates the tax number (NIP checksum or VAT id shape), fetches the party from the " +
			"White List (--nip) or VIES (--vat-id, with your seller NIP as requester so a consultation " +
			"number is stored), and only then appends a [parties.<alias>] table to the config; your " +
			"comments and formatting stay untouched. An existing alias is refused. Use --name and " +
			"--address when the registry has no name or address for the party (VIES often returns " +
			"\"---\"), or when the NIP is not on the White List at all (then both are needed and fv warns). Greece uses the VIES prefix EL, not GR. Exit code 2 means the registry lookup failed.",
		Example: "  fv party add acme --nip 5252248481\n" +
			"  fv party add berlin --vat-id DE123456789\n" +
			"  fv party add athens --vat-id EL123456783 --name \"Deigma A.E.\" --address \"Ermou 5, 105 63 Athina\"",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return s.runPartyAdd(cmd, args[0], nip, vatID, name, address)
		},
	}
	cmd.Flags().StringVar(&nip, "nip", "", "Polish NIP, 10 digits without separators (looked up in the White List)")
	cmd.Flags().StringVar(&vatID, "vat-id", "", "EU VAT id with country prefix, for example DE123456789 (looked up in VIES)")
	cmd.Flags().StringVar(&name, "name", "", "name to store in the config instead of the registry name (default: from the registry)")
	cmd.Flags().StringVar(&address, "address", "", "address line to store in the config instead of the registry address (default: from the registry)")
	return cmd
}

func (s *session) newPartyShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <alias>",
		Short: "Show a party's resolved data, where each field came from and the cache age",
		Long: "Prints the party as it would appear on an invoice: name, address, VAT status and bank " +
			"accounts, each tagged with its source ([whitelist], [vies] or [config]), the White List " +
			"request id or VIES consultation number, and how old the cached data is (marked stale when " +
			"older than registry.cache_ttl). It reads the cache and only contacts the registry when " +
			"there is no cached entry yet.",
		Example: "  fv party show acme",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return s.runPartyShow(cmd, args[0])
		},
	}
}

func (s *session) newPartyRefreshCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "refresh (<alias> | --all)",
		Short: "Fetch a party (or all parties) from the registry again",
		Long: "Fetches the party again from the White List or VIES and replaces the cached entry. " +
			"White List calls are counted per day; refreshing stops, and exits with code 2, at the " +
			"first refusal once 90 calls have been made that day (MF allows 100). Invoicing is never " +
			"blocked by this limit. A failed refresh keeps the old cache entry; with --all every party is tried and " +
			"exit code 2 means at least one failed.",
		Example: "  fv party refresh acme\n  fv party refresh --all",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if all == (len(args) == 1) {
				return errors.New("give either one party alias or --all")
			}
			return s.runPartyRefresh(cmd, args, all)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "refresh every party in the config, in alias order (default: only the named alias)")
	return cmd
}

func (s *session) runPartyAdd(cmd *cobra.Command, alias, nip, vatID, name, address string) error {
	if err := validate.PartyAlias(alias); err != nil {
		return err
	}
	vatID = strings.ToUpper(strings.ReplaceAll(vatID, " ", ""))
	if err := validate.PartyIdentity(nip, vatID); err != nil {
		return fmt.Errorf("--nip/--vat-id: %w", err)
	}
	if vatID != "" {
		if strings.HasPrefix(vatID, "PL") {
			return errors.New("--vat-id: a Polish party is added with --nip, not with a PL VAT id")
		}
		if _, _, _, err := registry.SplitVatID(vatID); err != nil {
			return fmt.Errorf("--vat-id: %w", err)
		}
	}
	rc, err := s.requireConfig(cmd)
	if err != nil {
		return err
	}
	if _, exists := rc.Config.Parties[alias]; exists {
		return fmt.Errorf("party %q already exists in %s; choose another alias or edit the config", alias, rc.Paths.ConfigFile)
	}
	if vatID != "" && rc.Config.Seller.NIP == "" {
		return errors.New("seller.nip is not set in the config; VIES needs it as the requester")
	}

	l, closeLog := s.logger(rc.Paths)
	defer closeLog()
	res := s.registry(rc, l, whiteListCounter(s, rc))
	entry := config.Party{NIP: nip, VatID: vatID, Name: name, Address: address}
	p, err := res.Resolve(cmd.Context(), alias, entry, s.deps.now(), registry.PurposeAdd)
	if err != nil {
		return err // nothing was written
	}

	old, err := os.ReadFile(rc.Paths.ConfigFile) // #nosec G304 -- the user's own config
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	if err := fsafe.WriteFile(rc.Paths.ConfigFile, appendPartyBlock(old, alias, entry)); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if _, err := config.Load(rc.Paths.ConfigFile); err != nil {
		if rerr := fsafe.WriteFile(rc.Paths.ConfigFile, old); rerr != nil {
			return fmt.Errorf("the new party block is invalid (%v) and the config could not be restored: %w", err, rerr)
		}
		return fmt.Errorf("the new party block is invalid, config restored: %w", err)
	}
	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "Added [parties.%s] to %s\n\n", alias, rc.Paths.ConfigFile)
	renderParty(out, p, s.deps.now(), s.ttlOf(rc))
	return nil
}

func (s *session) ttlOf(rc *runContext) int {
	if rc.Config.Registry.CacheTTL != nil {
		return *rc.Config.Registry.CacheTTL
	}
	return defaultCacheTTLDays
}

func (s *session) runPartyShow(cmd *cobra.Command, alias string) error {
	rc, err := s.requireConfig(cmd)
	if err != nil {
		return err
	}
	cp, ok := rc.Config.Parties[alias]
	if !ok {
		return fmt.Errorf("unknown party %q; known: %s", alias, knownAliases(rc.Config))
	}
	l, closeLog := s.logger(rc.Paths)
	defer closeLog()
	res := s.registry(rc, l, whiteListCounter(s, rc))
	p, err := res.Resolve(cmd.Context(), alias, cp, s.deps.now(), registry.PurposeShow)
	if err != nil {
		return err
	}
	renderParty(cmd.OutOrStdout(), p, s.deps.now(), s.ttlOf(rc))
	return nil
}

func (s *session) runPartyRefresh(cmd *cobra.Command, args []string, all bool) error {
	rc, err := s.requireConfig(cmd)
	if err != nil {
		return err
	}
	var aliases []string
	if all {
		for a := range rc.Config.Parties {
			aliases = append(aliases, a)
		}
		sort.Strings(aliases)
		if len(aliases) == 0 {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "no parties in the config; add one with fv party add")
			return nil
		}
	} else {
		if _, ok := rc.Config.Parties[args[0]]; !ok {
			return fmt.Errorf("unknown party %q; known: %s", args[0], knownAliases(rc.Config))
		}
		aliases = args
	}
	l, closeLog := s.logger(rc.Paths)
	defer closeLog()
	res := s.registry(rc, l, whiteListCounter(s, rc))
	out := cmd.OutOrStdout()
	failed := 0
	for _, a := range aliases {
		p, err := res.Resolve(cmd.Context(), a, rc.Config.Parties[a], s.deps.now(), registry.PurposeRefresh)
		switch {
		case err == nil:
			ref := p.RequestID
			if ref == "" {
				ref = p.ConsultationNumber
			}
			_, _ = fmt.Fprintf(out, "%s: refreshed (%s)\n", a, ref)
			for _, w := range p.Warnings {
				_, _ = fmt.Fprintf(out, "  warning: %s\n", w)
			}
		case errors.Is(err, registry.ErrQuota):
			_, _ = fmt.Fprintf(out, "%s: not refreshed\n", a)
			return err // stop at the first quota refusal
		case !all:
			return err
		default:
			failed++
			_, _ = fmt.Fprintf(out, "%s: FAILED: %v\n", a, err)
		}
	}
	if failed > 0 {
		return exitcode.Wrap(exitcode.ErrRegistry, fmt.Errorf("%d of %d parties could not be refreshed", failed, len(aliases)))
	}
	return nil
}

func knownAliases(c *config.Config) string {
	var a []string
	for k := range c.Parties {
		a = append(a, k)
	}
	if len(a) == 0 {
		return "none (add one with fv party add)"
	}
	sort.Strings(a)
	return strings.Join(a, ", ")
}

// renderParty prints the show view of p: each field with its source, the registry reference
// and the cache age.
func renderParty(w io.Writer, p registry.Party, now time.Time, ttlDays int) {
	line := func(label, value, src string) {
		if src != "" {
			value += "  [" + src + "]"
		}
		if label != "" {
			label += ":" // a continuation line (second account ...) has no label and no colon
		}
		_, _ = fmt.Fprintf(w, "%-12s %s\n", label, value)
	}
	line("Party", p.Alias, "")
	if p.NIP != "" {
		line("Identifier", "NIP "+p.NIP, "")
	} else {
		line("Identifier", "VAT id "+p.VatID, "")
	}
	line("Name", orDash(p.Name), p.Provenance["name"])
	line("Address", orDash(p.Address), p.Provenance["address"])
	if p.NIP != "" {
		line("VAT status", orDash(p.StatusVat), p.Provenance["statusVat"])
		if len(p.AccountNumbers) == 0 {
			line("Accounts", "none published", "")
		}
		for i, a := range p.AccountNumbers {
			label := ""
			if i == 0 {
				label = "Accounts"
			}
			line(label, a, p.Provenance["accounts"])
		}
		line("Request ID", orDash(p.RequestID), "White List")
	} else {
		line("Consultation", orDash(p.ConsultationNumber), "VIES")
	}
	cache := fmt.Sprintf("fetched %s (%s)", registry.AgeText(now, p.FetchedAt), p.FetchedAt.In(registry.Warsaw()).Format("2006-01-02 15:04"))
	if p.QueryDate != "" {
		cache += ", for date " + p.QueryDate
	}
	if p.Stale {
		cache += fmt.Sprintf(", stale (older than %d days; run fv party refresh %s)", ttlDays, p.Alias)
	}
	line("Cache", cache, "")
	for _, msg := range p.Warnings {
		_, _ = fmt.Fprintf(w, "warning: %s\n", msg)
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// appendPartyBlock returns config text with a [parties.<alias>] table appended. The
// existing bytes, comments included, are kept exactly.
func appendPartyBlock(old []byte, alias string, p config.Party) []byte {
	var b strings.Builder
	b.Write(old)
	if len(old) > 0 && old[len(old)-1] != '\n' {
		b.WriteByte('\n')
	}
	b.WriteString("\n[parties." + alias + "]\n")
	if p.NIP != "" {
		b.WriteString("nip = " + tomlString(p.NIP) + "\n")
	} else {
		b.WriteString("vat_id = " + tomlString(p.VatID) + "\n")
	}
	if p.Name != "" {
		b.WriteString("name = " + tomlString(p.Name) + "\n")
	}
	if p.Address != "" {
		b.WriteString("address = " + tomlString(p.Address) + "\n")
	}
	return []byte(b.String())
}

// tomlString renders s as a TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r < 0x20 || r == 0x7f:
			b.WriteString(`\u` + strings.ToUpper(fmt.Sprintf("%04x", r)))
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

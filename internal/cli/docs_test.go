package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/ksef"
	"github.com/lwasak/fv-cli/internal/money"
	"github.com/lwasak/fv-cli/internal/validate"
)

const docsDir = "../../docs"

// guideFiles are the pages of the user guide; every one must exist and be linked from the index.
var guideFiles = []string{
	"README.md", "install.md", "setup.md", "parties.md", "workflow.md", "amounts.md",
	"numbering-and-archive.md", "exit-codes.md", "troubleshooting.md", "security.md",
	"config-reference.md", "decisions.md",
}

func readDoc(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(docsDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDocs_GuideFilesExistAndAreIndexed(t *testing.T) {
	index := readDoc(t, "README.md")
	for _, f := range guideFiles {
		readDoc(t, f)
		if f != "README.md" && !strings.Contains(index, "]("+f+")") {
			t.Errorf("docs/README.md does not link to %s", f)
		}
	}
	all, err := filepath.Glob(filepath.Join(docsDir, "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, f := range guideFiles {
		known[f] = true
	}
	for _, p := range all {
		if !known[filepath.Base(p)] {
			t.Errorf("%s is not in the guide's file list (add it to guideFiles and docs/README.md)", filepath.Base(p))
		}
	}
}

var keyHeadingRe = regexp.MustCompile("^### `([^`]+)`\\s*$")
var keyFieldRe = regexp.MustCompile("^- (Type|Default|Allowed|Example): `([^`]*)`\\s*$")

// TestDocs_ConfigReferenceMatchesCode checks docs/config-reference.md against config.Reference
// in both directions: every key is documented with its type, default, allowed values and
// example, and the page documents no key the program does not know.
func TestDocs_ConfigReferenceMatchesCode(t *testing.T) {
	type doc struct{ typ, def, allowed, example string }
	got := map[string]*doc{}
	var cur *doc
	for _, line := range strings.Split(readDoc(t, "config-reference.md"), "\n") {
		if m := keyHeadingRe.FindStringSubmatch(line); m != nil {
			if got[m[1]] != nil {
				t.Errorf("config-reference.md documents %s twice", m[1])
			}
			cur = &doc{}
			got[m[1]] = cur
			continue
		}
		if strings.HasPrefix(line, "#") {
			cur = nil
			continue
		}
		if m := keyFieldRe.FindStringSubmatch(line); m != nil && cur != nil {
			switch m[1] {
			case "Type":
				cur.typ = m[2]
			case "Default":
				cur.def = m[2]
			case "Allowed":
				cur.allowed = m[2]
			case "Example":
				cur.example = m[2]
			}
		}
	}
	want := map[string]bool{}
	for _, e := range config.Reference {
		want[e.Key] = true
		d := got[e.Key]
		if d == nil {
			t.Errorf("config-reference.md lacks a ### `%s` section", e.Key)
			continue
		}
		for _, c := range []struct{ field, docs, code string }{
			{"Type", d.typ, e.Type}, {"Default", d.def, e.Default},
			{"Allowed", d.allowed, e.Allowed}, {"Example", d.example, e.Example},
		} {
			if c.docs != c.code {
				t.Errorf("config-reference.md, %s, %s: docs say %q, the program says %q", e.Key, c.field, c.docs, c.code)
			}
		}
	}
	for k := range got {
		if !want[k] {
			t.Errorf("config-reference.md documents %s, which is not a config key", k)
		}
	}
}

// TestDocs_PathsMatchCode checks the per-OS locations of the config reference against
// config.ResolvePaths.
func TestDocs_PathsMatchCode(t *testing.T) {
	page := readDoc(t, "config-reference.md")
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	cases := []struct {
		goos, cfg, data string
		getenv          map[string]string
		home            string
		inDocs          []string
	}{
		{"windows", `C:\Users\u\AppData\Roaming\fv\config.toml`, `C:\Users\u\AppData\Local\fv`,
			map[string]string{"AppData": `C:\Users\u\AppData\Roaming`, "LocalAppData": `C:\Users\u\AppData\Local`}, `C:\Users\u`,
			[]string{`%AppData%\fv\config.toml`, `%LocalAppData%\fv`}},
		{"darwin", "/Users/u/Library/Application Support/fv/config.toml", "/Users/u/Library/Application Support/fv",
			nil, "/Users/u", []string{"~/Library/Application Support/fv/config.toml"}},
		{"linux", "/home/u/.config/fv/config.toml", "/home/u/.local/share/fv",
			nil, "/home/u", []string{"~/.config/fv/config.toml", "~/.local/share/fv", "$XDG_CONFIG_HOME/fv/config.toml", "$XDG_DATA_HOME/fv"}},
	}
	for _, c := range cases {
		p, err := config.ResolvePaths(c.goos, env(c.getenv), c.home, "")
		if err != nil {
			t.Fatal(err)
		}
		if p.ConfigFile != c.cfg || p.DataDir != c.data {
			t.Errorf("%s: code resolves %q and %q, the test expects %q and %q", c.goos, p.ConfigFile, p.DataDir, c.cfg, c.data)
		}
		for _, s := range c.inDocs {
			if !strings.Contains(page, s) {
				t.Errorf("config-reference.md lacks the %s location %q", c.goos, s)
			}
		}
	}
	for _, v := range []string{"FV_CONFIG", "FV_DATA_DIR", "FV_SECRET_KSEF_"} {
		if !strings.Contains(page, v) {
			t.Errorf("config-reference.md does not mention %s", v)
		}
	}
}

// TestDocs_AmountsQuotingMatchesCode keeps the per-shell quoting advice of docs/amounts.md
// identical to money.QuotingAdvice, which is what fv help amounts prints.
func TestDocs_AmountsQuotingMatchesCode(t *testing.T) {
	if !strings.Contains(readDoc(t, "amounts.md"), money.QuotingAdvice) {
		t.Error("docs/amounts.md does not contain money.QuotingAdvice verbatim")
	}
}

// shellFields splits a command line into words the way a shell would for the simple cases
// the guide uses: double and single quotes group, a backslash is literal, and an unquoted
// pipe, redirect, semicolon, ampersand, closing parenthesis or comment ends the command.
func shellFields(s string) []string {
	var out []string
	var cur strings.Builder
	have := false
	flush := func() {
		if have {
			out = append(out, cur.String())
		}
		cur.Reset()
		have = false
	}
	var quote rune
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote, have = r, true
		case r == ' ' || r == '\t':
			flush()
		case r == '|' || r == '>' || r == '<' || r == ';' || r == '&' || r == ')':
			flush()
			return out
		case r == '#' && !have:
			return out
		default:
			cur.WriteRune(r)
			have = true
		}
	}
	flush()
	return out
}

var fvAtRe = regexp.MustCompile(`(^|[\s(])fv\s`)

// fvArgs returns the arguments after "fv" of the first fv command on line, or nil.
func fvArgs(line string) []string {
	loc := fvAtRe.FindStringSubmatchIndex(line)
	if loc == nil {
		return nil
	}
	f := shellFields(line[loc[3]:])
	for i, w := range f {
		if w == "fv" {
			return f[i+1:]
		}
	}
	return nil
}

// docCommands returns the fv command lines in the fenced blocks of a page, with their
// line numbers. Only lines that start with "fv " count, and blocks tagged text, toml or
// output are skipped (they show results and files, not commands).
func docCommands(page string) map[int]string {
	out := map[int]string{}
	inFence, skip := false, false
	for i, line := range strings.Split(page, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") {
			if inFence {
				inFence = false
				continue
			}
			info := strings.TrimSpace(strings.TrimPrefix(trim, "```"))
			inFence, skip = true, info == "text" || info == "toml" || info == "output"
			continue
		}
		if inFence && !skip && strings.HasPrefix(trim, "fv ") {
			out[i+1] = trim
		}
	}
	return out
}

// checkParses resolves args against a fresh command tree and parses its flags; nothing runs.
func checkParses(args []string) error {
	root := NewRootCmd(DefaultDeps())
	cmd, rest, err := root.Find(args)
	if err != nil {
		return err
	}
	if cmd == root {
		return fmt.Errorf("names no command")
	}
	if err := cmd.ParseFlags(rest); err != nil {
		return err
	}
	return cmd.ValidateArgs(cmd.Flags().Args())
}

// TestDocs_ExamplesParse parses every fv command line in the guide's fenced blocks and every
// command's Example against the real command tree: the command and its flags must exist and
// the arguments must fit. Nothing is executed.
func TestDocs_ExamplesParse(t *testing.T) {
	n := 0
	for _, f := range guideFiles {
		cmds := docCommands(readDoc(t, f))
		lines := make([]int, 0, len(cmds))
		for ln := range cmds {
			lines = append(lines, ln)
		}
		sort.Ints(lines)
		for _, ln := range lines {
			n++
			if err := checkParses(fvArgs(cmds[ln])); err != nil {
				t.Errorf("docs/%s:%d: %q does not parse: %v", f, ln, cmds[ln], err)
			}
		}
	}
	if n < 20 {
		t.Errorf("only %d fv command lines found in the guide; the extractor is probably broken", n)
	}

	m := 0
	root := NewRootCmd(DefaultDeps())
	walk(root, func(c *cobra.Command) {
		for _, line := range strings.Split(c.Example, "\n") {
			args := fvArgs(strings.TrimSpace(line))
			if args == nil {
				if strings.TrimSpace(line) != "" {
					t.Errorf("%s: example line %q has no fv command", c.CommandPath(), line)
				}
				continue
			}
			m++
			if err := checkParses(args); err != nil {
				t.Errorf("%s: example %q does not parse: %v", c.CommandPath(), strings.TrimSpace(line), err)
			}
		}
	})
	if m < 30 {
		t.Errorf("only %d command examples found; the extractor is probably broken", m)
	}
}

func TestDocs_ExtractorSelfCheck(t *testing.T) {
	got := fvArgs(`fv issue --to acme --net "18500,43" # comment`)
	if strings.Join(got, "|") != "issue|--to|acme|--net|18500,43" {
		t.Errorf("fvArgs = %q", got)
	}
	if got := fvArgs(`source <(fv completion zsh)`); strings.Join(got, "|") != "completion|zsh" {
		t.Errorf("fvArgs(source) = %q", got)
	}
	if got := fvArgs(`fv show FV/2026/10/001 --xml > invoice.xml`); strings.Join(got, "|") != "show|FV/2026/10/001|--xml" {
		t.Errorf("fvArgs(redirect) = %q", got)
	}
	for _, bad := range [][]string{{"nosuchcommand"}, {"issue", "--nosuchflag"}, {"send"}, {"send", "a", "b"}} {
		if checkParses(bad) == nil {
			t.Errorf("fv %v should not parse", bad)
		}
	}
	for _, ok := range [][]string{{"--env", "demo", "send", "FV/1/001"}, {"help", "party", "add"}, {"issue", "--to", "a", "--net", "1"}} {
		if err := checkParses(ok); err != nil {
			t.Errorf("fv %v: %v", ok, err)
		}
	}
}

var (
	linkRe      = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)\)`)
	headingRe   = regexp.MustCompile(`^#{1,6}\s+(.*?)\s*#*$`)
	inlineCode  = regexp.MustCompile("`[^`\n]*`")
	htmlTagRe   = regexp.MustCompile(`<[A-Za-z/!]`)
	transcriptC = regexp.MustCompile(`^<!-- /?transcript:[a-z0-9-]+ -->$`)
)

// slug turns a heading into its anchor the way Obsidian and GitHub do: lower case, letters,
// digits, hyphens and underscores kept, spaces to hyphens, everything else dropped.
func slug(h string) string {
	h = strings.ReplaceAll(h, "`", "")
	var b strings.Builder
	for _, r := range strings.ToLower(h) {
		switch {
		case r == ' ':
			b.WriteByte('-')
		case r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r > 127:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// proseLines returns the lines outside fenced code blocks, with inline code removed.
func proseLines(page string) []string {
	var out []string
	in := false
	for _, line := range strings.Split(page, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			in = !in
			continue
		}
		if !in {
			out = append(out, inlineCode.ReplaceAllString(line, ""))
		}
	}
	return out
}

// TestDocs_LinksAndMarkdown checks that every relative link and anchor in the guide resolves,
// and that the pages are plain Markdown: no HTML except the transcript marker comments.
func TestDocs_LinksAndMarkdown(t *testing.T) {
	anchors := map[string]map[string]bool{}
	for _, f := range guideFiles {
		set := map[string]bool{}
		for _, line := range proseLines(readDoc(t, f)) {
			if m := headingRe.FindStringSubmatch(line); m != nil {
				set[slug(m[1])] = true
			}
		}
		anchors[f] = set
	}
	for _, f := range guideFiles {
		page := readDoc(t, f)
		for i, line := range proseLines(page) {
			if htmlTagRe.MatchString(line) && !transcriptC.MatchString(strings.TrimSpace(line)) {
				t.Errorf("docs/%s: line %d contains HTML (%q); the guide is plain Markdown", f, i+1, strings.TrimSpace(line))
			}
			for _, m := range linkRe.FindAllStringSubmatch(line, -1) {
				target := m[1]
				if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
					continue
				}
				file, anchor, _ := strings.Cut(target, "#")
				if file == "" {
					file = f
				}
				set, ok := anchors[file]
				if !ok {
					t.Errorf("docs/%s: link %q points at a page that is not in the guide", f, target)
					continue
				}
				if anchor != "" && !set[anchor] {
					t.Errorf("docs/%s: link %q: %s has no heading with that anchor", f, target, file)
				}
			}
		}
	}
}

func TestDocs_KSeFNumbersHaveAValidChecksum(t *testing.T) {
	files := []string{"../../README.md"}
	for _, f := range guideFiles {
		files = append(files, filepath.Join(docsDir, f))
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range ksefNumberToken.FindAllString(string(b), -1) {
			if err := ksef.ValidateKSeFNumber(n); err != nil {
				t.Errorf("%s: %s: %v", filepath.Base(f), n, err)
			}
		}
	}
}

// TestDocs_ConfigExampleIsAValidConfig loads the example config of docs/config-reference.md
// the way fv does (strictly) and validates it.
func TestDocs_ConfigExampleIsAValidConfig(t *testing.T) {
	page := readDoc(t, "config-reference.md")
	_, after, ok := strings.Cut(page, "## Example\n")
	if !ok {
		t.Fatal("config-reference.md has no Example section")
	}
	_, body, ok := strings.Cut(after, "```toml\n")
	if !ok {
		t.Fatal("the Example section has no toml block")
	}
	toml, _, _ := strings.Cut(body, "```")
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("the example does not load: %v", err)
	}
	if findings := validate.Config(cfg); len(findings) > 0 {
		t.Errorf("the example config has findings: %v", findings)
	}
	if len(cfg.Items) == 0 || cfg.Defaults.SWIFT == "" {
		t.Error("the example should show items and the bank fields")
	}
}

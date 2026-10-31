package numbering

import (
	"testing"
	"time"
)

func d(y int, m time.Month, day int) time.Time {
	return time.Date(y, m, day, 12, 0, 0, 0, time.UTC)
}

func TestRender(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		issue   time.Time
		seq     int
		want    string
	}{
		{"default", DefaultPattern, d(2026, 10, 31), 3, "FV/2026/10/003"},
		{"dec31", DefaultPattern, d(2026, 12, 31), 12, "FV/2026/12/012"},
		{"jan1", DefaultPattern, d(2027, 1, 1), 1, "FV/2027/01/001"},
		{"jan31", "{YYYY}-{MM}-{SEQ}", d(2026, 1, 31), 7, "2026-01-7"},
		{"feb1", "{YYYY}-{MM}-{SEQ}", d(2026, 2, 1), 1, "2026-02-1"},
		{"leap day", "{SEQ:2}/{DD}.{MM}.{YYYY}", d(2028, 2, 29), 5, "05/29.02.2028"},
		{"yy", "FV{YY}{MM}{SEQ:4}", d(2026, 3, 9), 42, "FV26030042"},
		{"yy 2005", "{YY}/{SEQ}", d(2005, 3, 9), 1, "05/1"},
		{"wider not truncated", "FV/{YYYY}/{MM}/{SEQ:3}", d(2026, 10, 1), 1000, "FV/2026/10/1000"},
		{"width 1", "{SEQ:1}", d(2026, 10, 1), 12, "12"},
		{"literals", "A_b.c-{SEQ}", d(2026, 10, 1), 9, "A_b.c-9"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Render(tt.pattern, tt.issue, tt.seq)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRender_Errors(t *testing.T) {
	for _, p := range []string{"", "FV/{YYYY}", "{SEQ}{SEQ}", "{seq:03}", "{SEQ:0}", "a b {SEQ}", "{FOO}{SEQ}"} {
		if _, err := Render(p, d(2026, 1, 1), 1); err == nil {
			t.Errorf("pattern %q: expected error", p)
		}
	}
	if _, err := Render("{SEQ}", d(2026, 1, 1), 0); err == nil {
		t.Error("seq 0: expected error")
	}
}

func TestPeriodKey_BoundariesByIssueDate(t *testing.T) {
	tests := []struct {
		r     Reset
		issue time.Time
		want  string
	}{
		{ResetMonthly, d(2026, 12, 31), "2026-12"},
		{ResetMonthly, d(2027, 1, 1), "2027-01"},
		{ResetMonthly, d(2026, 1, 31), "2026-01"},
		{ResetMonthly, d(2026, 2, 1), "2026-02"},
		{ResetMonthly, d(2028, 2, 29), "2028-02"},
		{ResetYearly, d(2026, 12, 31), "2026"},
		{ResetYearly, d(2027, 1, 1), "2027"},
		{ResetNever, d(2027, 1, 1), "all"},
	}
	for _, tt := range tests {
		if got := PeriodKey(tt.r, tt.issue); got != tt.want {
			t.Errorf("PeriodKey(%s, %s) = %q, want %q", tt.r, tt.issue.Format("2006-01-02"), got, tt.want)
		}
	}
}

func TestDefaultReset(t *testing.T) {
	tests := map[string]Reset{
		DefaultPattern:        ResetMonthly,
		"FV/{YYYY}/{SEQ}":     ResetYearly,
		"{YY}-{SEQ:3}":        ResetYearly,
		"FV/{SEQ:4}":          ResetNever,
		"{MM}/{SEQ}":          ResetNever,
		"{DD}.{MM}/{SEQ:2}":   ResetNever,
		"{YYYY}{MM}{DD}{SEQ}": ResetMonthly,
	}
	for p, want := range tests {
		if got := DefaultReset(p); got != want {
			t.Errorf("DefaultReset(%q) = %q, want %q", p, got, want)
		}
	}
}

func TestCheckResetFits(t *testing.T) {
	if err := CheckResetFits(DefaultPattern, ResetMonthly); err != nil {
		t.Error(err)
	}
	if err := CheckResetFits("FV/{YYYY}/{SEQ}", ResetMonthly); err == nil {
		t.Error("monthly without {MM} must be refused")
	}
	if err := CheckResetFits("FV/{SEQ}", ResetYearly); err == nil {
		t.Error("yearly without year must be refused")
	}
	if err := CheckResetFits("FV/{SEQ}", ResetNever); err != nil {
		t.Error(err)
	}
	for _, p := range []string{DefaultPattern, "FV/{YYYY}/{SEQ}", "{MM}/{SEQ}", "FV/{SEQ}"} {
		if err := CheckResetFits(p, DefaultReset(p)); err != nil {
			t.Errorf("DefaultReset(%q) does not fit: %v", p, err)
		}
	}
}

func TestParseReset(t *testing.T) {
	for _, s := range []string{"monthly", "yearly", "never"} {
		if r, err := ParseReset(s); err != nil || string(r) != s {
			t.Errorf("ParseReset(%q) = %q, %v", s, r, err)
		}
	}
	if _, err := ParseReset("daily"); err == nil {
		t.Error("daily must be refused")
	}
}

func TestSanitize(t *testing.T) {
	if got := Sanitize(`FV/2026\10/001`); got != "FV_2026_10_001" {
		t.Errorf("got %q", got)
	}
}

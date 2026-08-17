package templates

import (
	"strings"
	"testing"
	"time"
)

// TestAddedLabel pins the wording at each boundary (plan §14). The switch from
// an age to a date is the whole feature: an age answers "does the owner still
// want this", a date answers "should I ask first", and the label has to be the
// one that fits.
func TestAddedLabel(t *testing.T) {
	now := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339) }

	cases := []struct {
		name string
		ts   string
		want string
	}{
		{"minutes old", ago(20 * time.Minute), "Added today"},
		{"same day", ago(20 * time.Hour), "Added today"},
		{"just over a day", ago(30 * time.Hour), "Added yesterday"},
		{"three days", ago(3 * 24 * time.Hour), "Added 3 days ago"},
		{"one day plural boundary", ago(47 * time.Hour), "Added yesterday"},
		{"six days", ago(6 * 24 * time.Hour), "Added 6 days ago"},
		{"one week", ago(8 * 24 * time.Hour), "Added 1 week ago"},
		{"three weeks", ago(22 * 24 * time.Hour), "Added 3 weeks ago"},
		{"a season ago is a date", ago(120 * 24 * time.Hour), "Added April 19, 2026"},
		{"years ago is a date", ago(3 * 365 * 24 * time.Hour), "Added August 18, 2023"},
		{"unparseable is silent", "not a timestamp", ""},
		{"empty is silent", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := addedLabelAt(tc.ts, now); got != tc.want {
				t.Errorf("addedLabelAt(%q) = %q, want %q", tc.ts, got, tc.want)
			}
		})
	}
}

// TestAddedLabelFutureTimestampSaysADate: a skewed clock or a bad imported row
// must not produce "Added in -3 days".
func TestAddedLabelFutureTimestampSaysADate(t *testing.T) {
	now := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	got := addedLabelAt(now.Add(72*time.Hour).Format(time.RFC3339), now)

	if strings.Contains(got, "ago") || strings.Contains(got, "-") {
		t.Errorf("addedLabelAt(future) = %q, want a plain date", got)
	}
	if !strings.HasPrefix(got, "Added ") {
		t.Errorf("addedLabelAt(future) = %q, want an Added date", got)
	}
}

// TestExactTimestamp is what the title attribute carries when the label itself
// is relative: anyone who wants the precise answer hovers for it.
func TestExactTimestamp(t *testing.T) {
	ts := time.Date(2024, time.March, 12, 15, 4, 0, 0, time.UTC)
	got := exactTimestamp(ts.Format(time.RFC3339))
	want := ts.Local().Format("January 2, 2006 at 3:04 PM")

	if got != want {
		t.Errorf("exactTimestamp = %q, want %q", got, want)
	}
	if exactTimestamp("nonsense") != "" {
		t.Error("a bad timestamp should render nothing, not a zero date")
	}
}

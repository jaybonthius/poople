package ladder

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func loadData(t *testing.T) *Data {
	t.Helper()
	d, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return d
}

func TestLoadCounts(t *testing.T) {
	d := loadData(t)
	tests := []struct {
		name string
		got  int
		want int
	}{
		{"accepted words", len(d.Words()), 5662},
		{"reachable from poop", d.Reachable(), 5586},
		{"calendar", len(d.Calendar()), 1092},
		{"unlimited pool", len(d.Unlimited()), 2201},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %d, want %d", tt.name, tt.got, tt.want)
		}
	}
}

func TestDistance(t *testing.T) {
	d := loadData(t)
	tests := []struct {
		word      string
		want      int
		reachable bool
	}{
		{"poop", 0, true},
		{"poos", 1, true},
		{"tips", 4, true},
		{"dawn", 4, true},
		{"fuse", 5, true},
		{"boop", 0, false}, // not in CSW24
		{"evil", 0, false}, // accepted but isolated
	}
	for _, tt := range tests {
		got, ok := d.Distance(tt.word)
		if ok != tt.reachable || got != tt.want {
			t.Errorf("Distance(%q) = %d, %v; want %d, %v", tt.word, got, ok, tt.want, tt.reachable)
		}
	}
}

func TestValid(t *testing.T) {
	d := loadData(t)
	tests := []struct {
		word string
		want bool
	}{
		{"poop", true},
		{"tips", true},
		{"evil", true},
		{"boop", false},
		{"POOP", false},
		{"poo", false},
		{"poops", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := d.Valid(tt.word); got != tt.want {
			t.Errorf("Valid(%q) = %v, want %v", tt.word, got, tt.want)
		}
	}
}

func TestOneLetterApart(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"poop", "poos", true},
		{"poop", "boop", true},
		{"poop", "poop", false},
		{"poop", "pops", false},
		{"poop", "poo", false},
		{"tips", "pits", false},
	}
	for _, tt := range tests {
		if got := OneLetterApart(tt.a, tt.b); got != tt.want {
			t.Errorf("OneLetterApart(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestNeighbors(t *testing.T) {
	d := loadData(t)
	for _, w := range []string{"poop", "tips", "boop"} {
		got := d.Neighbors(w)
		if len(got) == 0 {
			t.Errorf("Neighbors(%q) is empty", w)
		}
		for i, n := range got {
			if !d.Valid(n) || !OneLetterApart(w, n) {
				t.Errorf("Neighbors(%q) includes %q", w, n)
			}
			if i > 0 && got[i-1] >= n {
				t.Errorf("Neighbors(%q) not sorted: %v", w, got)
			}
		}
		if strings.Contains(strings.Join(got, ","), w) {
			t.Errorf("Neighbors(%q) includes itself", w)
		}
	}
	if got := d.Neighbors("evil"); len(got) != 0 {
		t.Errorf("Neighbors(evil) = %v, want none", got)
	}
}

func TestShortestPathEveryCalendarWord(t *testing.T) {
	d := loadData(t)
	for _, start := range d.Calendar() {
		dist, _ := d.Distance(start)
		path, ok := d.ShortestPath(start)
		if !ok || len(path) != dist+1 {
			t.Errorf("ShortestPath(%q) = %v, %v; want %d words", start, path, ok, dist+1)
			continue
		}
		if path[0] != start || path[len(path)-1] != Target {
			t.Errorf("ShortestPath(%q) = %v, want %s ... %s", start, path, start, Target)
		}
		for i, w := range path {
			if !d.Valid(w) {
				t.Errorf("ShortestPath(%q) step %q not accepted", start, w)
			}
			if i > 0 && !OneLetterApart(path[i-1], w) {
				t.Errorf("ShortestPath(%q) step %q -> %q not one letter apart", start, path[i-1], w)
			}
		}
	}
}

func TestShortestPath(t *testing.T) {
	d := loadData(t)
	if got, ok := d.ShortestPath("poop"); !ok || len(got) != 1 || got[0] != "poop" {
		t.Errorf("ShortestPath(poop) = %v, %v; want [poop]", got, ok)
	}
	for _, w := range []string{"boop", "evil"} {
		if got, ok := d.ShortestPath(w); ok {
			t.Errorf("ShortestPath(%q) = %v, want unreachable", w, got)
		}
	}
	first, _ := d.ShortestPath("fuse")
	second, _ := d.ShortestPath("fuse")
	if strings.Join(first, " ") != strings.Join(second, " ") {
		t.Errorf("ShortestPath(fuse) not deterministic: %v then %v", first, second)
	}
}

func TestCalendarAndUnlimitedDistances(t *testing.T) {
	d := loadData(t)
	for name, words := range map[string][]string{"calendar": d.Calendar(), "unlimited": d.Unlimited()} {
		for _, w := range words {
			if dist, ok := d.Distance(w); !ok || dist < MinStartDistance {
				t.Errorf("%s word %q has distance %d, %v", name, w, dist, ok)
			}
		}
	}
	for _, w := range d.Unlimited() {
		if !d.Common(w) {
			t.Errorf("unlimited word %q is not common", w)
		}
	}
	if d.Common("boop") {
		t.Error("Common(boop) = true for a word outside the accepted list")
	}
}

func TestDailyNumber(t *testing.T) {
	tests := []struct {
		at   string
		want int
	}{
		{"2025-08-14T08:00:00Z", 1}, // before launch: still #1, never #0
		{"2026-10-11T07:59:59Z", 1},
		{"2026-10-11T08:00:00Z", 1},
		{"2026-10-12T07:59:59Z", 1},
		{"2026-10-12T08:00:00Z", 2},
		{"2026-10-13T07:59:59Z", 2},
		{"2026-10-13T08:00:00Z", 3},
		{"2026-10-12T10:00:00+02:00", 2}, // 08:00 UTC
		{"2029-10-07T08:00:00Z", 1093},   // 1,092 days after launch: the calendar wraps
	}
	for _, tt := range tests {
		at, err := time.Parse(time.RFC3339, tt.at)
		if err != nil {
			t.Fatal(err)
		}
		if got := DailyNumber(at); got != tt.want {
			t.Errorf("DailyNumber(%s) = %d, want %d", tt.at, got, tt.want)
		}
	}
}

func TestDailyOpens(t *testing.T) {
	for n, want := range map[int]string{1: "2026-10-11T08:00:00Z", 2: "2026-10-12T08:00:00Z", 1093: "2029-10-07T08:00:00Z"} {
		if got := DailyOpens(n).UTC().Format(time.RFC3339); got != want {
			t.Errorf("DailyOpens(%d) = %s, want %s", n, got, want)
		}
		if got := DailyNumber(DailyOpens(n).Add(time.Second)); got != n {
			t.Errorf("DailyNumber just after DailyOpens(%d) = %d", n, got)
		}
		if got := DailyNumber(DailyOpens(n + 1).Add(-time.Second)); got != n {
			t.Errorf("DailyNumber just before DailyOpens(%d) = %d", n+1, got)
		}
	}
}

func TestDailyStart(t *testing.T) {
	d := loadData(t)
	calendar := d.Calendar()
	size := len(calendar)
	if size != 1092 {
		t.Fatalf("calendar length = %d, want 1092", size)
	}
	tests := []struct {
		n    int
		want string
		par  int
	}{
		{1, "pave", 5},
		{2, "long", 4},
		{3, "tent", 4},
		{size, "levy", 6},
		{size + 1, "pave", 5}, // wraps to the first entry
		{size + 2, "long", 4},
	}
	for _, tt := range tests {
		got := d.DailyStart(tt.n)
		if got != tt.want {
			t.Errorf("DailyStart(%d) = %q, want %q", tt.n, got, tt.want)
		}
		if par, _ := d.Distance(got); par != tt.par {
			t.Errorf("par of #%d %q = %d, want %d", tt.n, got, par, tt.par)
		}
	}
	for n := 1; n <= size; n++ {
		if got := d.DailyStart(n); got != calendar[n-1] {
			t.Fatalf("DailyStart(%d) = %q, want calendar entry %d %q", n, got, n-1, calendar[n-1])
		}
	}
}

// spoils reports whether yesterday's revealed shortest ladder gives away
// today's start word: it contains the word or a word one letter from it.
func spoils(d *Data, yesterday, today string) bool {
	path, _ := d.ShortestPath(yesterday)
	for _, w := range path {
		if w == today || OneLetterApart(w, today) {
			return true
		}
	}
	return false
}

func violations(d *Data, order []string) []string {
	var out []string
	for i, yesterday := range order {
		today := order[(i+1)%len(order)]
		if spoils(d, yesterday, today) || OneLetterApart(yesterday, today) {
			out = append(out, yesterday+"->"+today)
		}
	}
	return out
}

func TestCalendarKeepsYesterdaysAnswerOffToday(t *testing.T) {
	d := loadData(t)
	calendar := d.Calendar()
	if bad := violations(d, calendar); len(bad) != 0 {
		t.Errorf("consecutive days spoil each other, including the wrap: %v", bad)
	}
	// The repair is exercised: the plain shuffle breaks the rule.
	shuffled := shuffleCalendar(calendar)
	if bad := violations(d, shuffled); len(bad) == 0 {
		t.Error("the fixed-seed shuffle already satisfies the rule; this test no longer covers the repair")
	}

	// Same start words as Poople's calendar filtered to distance >= 4, in a new order.
	raw, err := dataFiles.ReadFile("data/poople-calendar.txt")
	if err != nil {
		t.Fatal(err)
	}
	var filtered []string
	for _, w := range strings.Fields(string(raw)) {
		if dist, ok := d.Distance(w); ok && dist >= MinStartDistance {
			filtered = append(filtered, w)
		}
	}
	if !slices.Equal(slices.Sorted(slices.Values(filtered)), slices.Sorted(slices.Values(calendar))) {
		t.Error("calendar words differ from Poople's start words with distance >= 4")
	}
	if slices.Equal(filtered[:10], calendar[:10]) {
		t.Error("calendar still follows Poople's order")
	}

	again := loadData(t).Calendar()
	if !slices.Equal(again, calendar) {
		t.Error("calendar order changed between loads")
	}
}

func TestCalendarRefusesOrderThatSpoils(t *testing.T) {
	// mass and mast are one letter apart, so neither may follow the other.
	const accepted = "poop\npoos\nposs\npass\nmass\nmast\nmist\n"
	if _, err := Parse([]byte(accepted), []byte("mass\nmast\n"), nil); err == nil {
		t.Error("Parse accepted a calendar whose only two words are one letter apart")
	}
	// mist's shortest ladder passes through mass, so mist cannot precede mass,
	// and a two-word calendar wraps.
	if _, err := Parse([]byte(accepted), []byte("mass\nmist\n"), nil); err == nil {
		t.Error("Parse accepted a calendar where yesterday's ladder contains today's word")
	}
	// One word has no other day to spoil.
	if _, err := Parse([]byte(accepted), []byte("mist\n"), nil); err != nil {
		t.Errorf("Parse of a one-word calendar: %v", err)
	}
}

func TestChecksum(t *testing.T) {
	d := loadData(t)
	const want = "abb39493550b7332eb1dfb7c0a612865a19bc5b73398e16d1cf7f8194028ded7"
	if got := d.Checksum(); got != want {
		t.Errorf("Checksum() = %s, want %s; update when the word lists or calendarOrderVersion change", got, want)
	}
	// Databases loaded before the calendar was reordered hold this checksum;
	// the new order must arrive as a new list version.
	const poopleOrder = "e09353906bb192e316944e4bc5af73861500f357e4e755d1733fb4a892583b4f"
	if d.Checksum() == poopleOrder {
		t.Error("Checksum() matches the Poople-order version, so existing databases keep the old calendar")
	}
	again := loadData(t)
	if again.Checksum() != d.Checksum() {
		t.Errorf("Checksum changed between loads")
	}

	// Moving a word between files must change the checksum.
	ladder := []byte("poop\npoos\nposs\ntoss\ntoes\n")
	x, err := Parse(ladder, []byte("toes\n"), []byte("toss\n"))
	if err != nil {
		t.Fatal(err)
	}
	y, err := Parse(ladder, []byte("toes\ntoss\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if x.Checksum() == y.Checksum() {
		t.Error("checksum ignores file boundaries")
	}
}

func TestParseRejectsBadInput(t *testing.T) {
	const accepted, calendar, common = "poop\npoos\nposs\ntoss\ntoes\n", "toes\n", "toss\n"
	if _, err := Parse([]byte(accepted), []byte(calendar), []byte(common)); err != nil {
		t.Fatalf("Parse of valid input: %v", err)
	}
	tests := []struct {
		name                       string
		accepted, calendar, common string
	}{
		{"uppercase", "POOP\n" + accepted, calendar, common},
		{"short word", "poo\n" + accepted, calendar, common},
		{"blank line", accepted + "\n", calendar, common},
		{"carriage return", "dawn\r\n" + accepted, calendar, common},
		{"duplicate", accepted + "toes\n", calendar, common},
		{"missing target", "boop\n", "boop\n", common},
		{"bad calendar", accepted, "Toes\n", common},
		{"duplicate calendar", accepted, "toes\ntoes\n", common},
		{"bad common", accepted, calendar, "it\n"},
		{"no calendar word far enough", accepted, "toss\n", common},
	}
	for _, tt := range tests {
		if _, err := Parse([]byte(tt.accepted), []byte(tt.calendar), []byte(tt.common)); err == nil {
			t.Errorf("%s: Parse succeeded", tt.name)
		}
	}
}

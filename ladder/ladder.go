// Package ladder computes Poople's word graph from the embedded word lists:
// accepted words, BFS distance to the target POOP, the daily calendar and the
// unlimited pool. It holds no state beyond the immutable value built by Load;
// the app stores the results in SQLite.
package ladder

import (
	"crypto/sha256"
	"embed"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Target is the word every ladder ends on.
const Target = "poop"

// MinStartDistance is the smallest distance to Target a start word may have.
// Calendar and unlimited start words closer than this are skipped.
const MinStartDistance = 4

// dailyEpoch is when puzzle 0 started; a new daily puzzle starts every 24 hours.
var dailyEpoch = time.Date(2025, time.August, 14, 8, 0, 0, 0, time.UTC)

//go:embed data/csw24-four-letter.txt data/poople-calendar.txt data/poople-common.txt
var dataFiles embed.FS

// Data is an immutable view of one version of the word lists.
type Data struct {
	words     []string // accepted words, sorted
	accepted  map[string]bool
	distance  map[string]int // BFS distance to Target; reachable words only
	common    map[string]bool
	calendar  []string // Poople calendar order, distance >= MinStartDistance
	unlimited []string // common accepted words with distance >= MinStartDistance, sorted
	checksum  string
}

// Load parses the embedded word lists.
func Load() (*Data, error) {
	var files [3][]byte
	for i, name := range []string{"data/csw24-four-letter.txt", "data/poople-calendar.txt", "data/poople-common.txt"} {
		b, err := dataFiles.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		files[i] = b
	}
	return Parse(files[0], files[1], files[2])
}

// Parse builds Data from the accepted-word list, the Poople calendar and the
// Poople frequency list. Each file holds one lowercase four-letter word per
// line with no duplicates. The accepted list must contain Target.
func Parse(accepted, calendar, common []byte) (*Data, error) {
	acceptedWords, err := parseWords("accepted", accepted)
	if err != nil {
		return nil, err
	}
	calendarWords, err := parseWords("calendar", calendar)
	if err != nil {
		return nil, err
	}
	commonWords, err := parseWords("common", common)
	if err != nil {
		return nil, err
	}

	d := &Data{
		words:    slices.Sorted(slices.Values(acceptedWords)),
		accepted: make(map[string]bool, len(acceptedWords)),
		common:   make(map[string]bool),
		checksum: checksum(accepted, calendar, common),
	}
	for _, w := range acceptedWords {
		d.accepted[w] = true
	}
	if !d.accepted[Target] {
		return nil, fmt.Errorf("accepted list lacks target %q", Target)
	}
	for _, w := range commonWords {
		if d.accepted[w] {
			d.common[w] = true
		}
	}
	d.distance = d.distances()

	for _, w := range calendarWords {
		if dist, ok := d.distance[w]; ok && dist >= MinStartDistance {
			d.calendar = append(d.calendar, w)
		}
	}
	if len(d.calendar) == 0 {
		return nil, fmt.Errorf("calendar has no word with distance >= %d", MinStartDistance)
	}
	for _, w := range d.words {
		if dist, ok := d.distance[w]; ok && d.common[w] && dist >= MinStartDistance {
			d.unlimited = append(d.unlimited, w)
		}
	}
	return d, nil
}

func parseWords(name string, b []byte) ([]string, error) {
	var words []string
	seen := make(map[string]bool)
	lines := strings.Split(string(b), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for i, w := range lines {
		if !wellFormed(w) {
			return nil, fmt.Errorf("%s list line %d: %q is not four lowercase letters", name, i+1, w)
		}
		if seen[w] {
			return nil, fmt.Errorf("%s list line %d: duplicate %q", name, i+1, w)
		}
		seen[w] = true
		words = append(words, w)
	}
	return words, nil
}

func wellFormed(w string) bool {
	if len(w) != 4 {
		return false
	}
	for i := range len(w) {
		if w[i] < 'a' || w[i] > 'z' {
			return false
		}
	}
	return true
}

// checksum is the hex SHA-256 of each file's length (8-byte big-endian)
// followed by its bytes, in accepted, calendar, common order.
func checksum(files ...[]byte) string {
	h := sha256.New()
	for _, f := range files {
		h.Write(binary.BigEndian.AppendUint64(nil, uint64(len(f))))
		h.Write(f)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (d *Data) distances() map[string]int {
	dist := map[string]int{Target: 0}
	queue := []string{Target}
	for len(queue) > 0 {
		w := queue[0]
		queue = queue[1:]
		for _, n := range d.Neighbors(w) {
			if _, ok := dist[n]; !ok {
				dist[n] = dist[w] + 1
				queue = append(queue, n)
			}
		}
	}
	return dist
}

// Checksum identifies this version of the word lists.
func (d *Data) Checksum() string { return d.checksum }

// Words returns every accepted word, sorted.
func (d *Data) Words() []string { return slices.Clone(d.words) }

// Valid reports whether word is an accepted word. Words are lowercase.
func (d *Data) Valid(word string) bool { return d.accepted[word] }

// Common reports whether word is accepted and in the Poople frequency list.
func (d *Data) Common(word string) bool { return d.common[word] }

// Distance returns the fewest steps from word to Target, and false when word
// is not accepted or cannot reach Target.
func (d *Data) Distance(word string) (int, bool) {
	dist, ok := d.distance[word]
	return dist, ok
}

// Reachable returns the number of accepted words that can reach Target.
func (d *Data) Reachable() int { return len(d.distance) }

// Calendar returns the daily start words in order.
func (d *Data) Calendar() []string { return slices.Clone(d.calendar) }

// Unlimited returns the unlimited-mode start words, sorted.
func (d *Data) Unlimited() []string { return slices.Clone(d.unlimited) }

// Neighbors returns the accepted words one letter apart from word, sorted.
// word itself need not be accepted.
func (d *Data) Neighbors(word string) []string {
	var out []string
	b := []byte(word)
	for i := range b {
		orig := b[i]
		for c := byte('a'); c <= 'z'; c++ {
			if c == orig {
				continue
			}
			b[i] = c
			if d.accepted[string(b)] {
				out = append(out, string(b))
			}
		}
		b[i] = orig
	}
	slices.Sort(out)
	return out
}

// OneLetterApart reports whether a and b have equal length and differ in
// exactly one position.
func OneLetterApart(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	diff := 0
	for i := range len(a) {
		if a[i] != b[i] {
			diff++
		}
	}
	return diff == 1
}

// ShortestPath returns one shortest ladder from start to Target, including
// both ends, and false when start cannot reach Target. At each step it takes
// the alphabetically first neighbour one step closer, so the result is
// deterministic.
func (d *Data) ShortestPath(start string) ([]string, bool) {
	dist, ok := d.distance[start]
	if !ok {
		return nil, false
	}
	path := []string{start}
	for w := start; dist > 0; dist-- {
		for _, n := range d.Neighbors(w) {
			if nd, ok := d.distance[n]; ok && nd == dist-1 {
				w = n
				break
			}
		}
		path = append(path, w)
	}
	return path, true
}

// DailyNumber returns the daily puzzle number at t: whole days since
// 2025-08-14 08:00 UTC, or 0 before then.
func DailyNumber(t time.Time) int {
	if !t.After(dailyEpoch) {
		return 0
	}
	return int(t.Sub(dailyEpoch) / (24 * time.Hour))
}

// DailyStart returns the start word of daily puzzle n; the calendar wraps.
func (d *Data) DailyStart(n int) string {
	size := len(d.calendar)
	return d.calendar[(n%size+size)%size]
}

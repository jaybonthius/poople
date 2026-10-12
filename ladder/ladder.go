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

// dailyEpoch is when daily puzzle #1 opens; a new daily puzzle opens every 24
// hours.
var dailyEpoch = time.Date(2026, time.October, 11, 8, 0, 0, 0, time.UTC)

// calendarSeed fixes the calendar shuffle.
const calendarSeed = 0x706f6f706c65 // "poople"

// calendarOrderVersion names the calendar ordering rule. The order is derived
// from the files, so it is part of the checksum: bump it whenever orderCalendar
// changes so existing databases store the new order as a new list version.
const calendarOrderVersion = "calendar-order-1"

//go:embed data/csw24-four-letter.txt data/poople-calendar.txt data/poople-common.txt
var dataFiles embed.FS

// Data is an immutable view of one version of the word lists.
type Data struct {
	words     []string // accepted words, sorted
	accepted  map[string]bool
	distance  map[string]int // BFS distance to Target; reachable words only
	common    map[string]bool
	calendar  []string // daily start words in puzzle order; see orderCalendar
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
// line with no duplicates. The accepted list must contain Target. The calendar
// file supplies the set of start words, not their order; see orderCalendar.
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
		checksum: checksum([]byte(calendarOrderVersion), accepted, calendar, common),
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

	var starts []string
	for _, w := range calendarWords {
		if dist, ok := d.distance[w]; ok && dist >= MinStartDistance {
			starts = append(starts, w)
		}
	}
	if len(starts) == 0 {
		return nil, fmt.Errorf("calendar has no word with distance >= %d", MinStartDistance)
	}
	if d.calendar, err = d.orderCalendar(starts); err != nil {
		return nil, err
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

// checksum is the hex SHA-256 of each part's length (8-byte big-endian)
// followed by its bytes, in calendarOrderVersion, accepted, calendar, common
// order.
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

// orderCalendar returns starts in daily puzzle order. It sorts them, shuffles
// them with a fixed-seed generator so the order is the same on every machine
// and run, then repairs the order so that for every consecutive pair
// (yesterday, today), including the wrap from the last word to the first,
// yesterday's ShortestPath, which the next day reveals, contains neither
// today's start word nor any word one letter from it. That also keeps today's
// start word more than one letter from yesterday's.
//
// The repair walks the shuffled words and appends the first remaining word
// that may follow the last one appended; the final word must also precede the
// first. A word that fits nowhere at the end is inserted at the first earlier
// gap where it may follow and precede its neighbours.
func (d *Data) orderCalendar(starts []string) ([]string, error) {
	pool := shuffleCalendar(starts)
	if len(pool) < 2 {
		return pool, nil
	}
	paths := make(map[string][]string, len(pool))
	for _, w := range pool {
		paths[w], _ = d.ShortestPath(w)
	}
	mayFollow := func(yesterday, today string) bool {
		for _, w := range paths[yesterday] {
			if w == today || OneLetterApart(w, today) {
				return false
			}
		}
		return true
	}

	out := []string{pool[0]}
	pool = pool[1:]
	for len(pool) > 0 {
		last := out[len(out)-1]
		i := slices.IndexFunc(pool, func(w string) bool {
			return mayFollow(last, w) && (len(pool) > 1 || mayFollow(w, out[0]))
		})
		if i >= 0 {
			out = append(out, pool[i])
			pool = slices.Delete(pool, i, i+1)
			continue
		}
		w := pool[0]
		k := 1
		for ; k < len(out); k++ {
			if mayFollow(out[k-1], w) && mayFollow(w, out[k]) {
				break
			}
		}
		if k == len(out) {
			return nil, fmt.Errorf("calendar: no place for %q that keeps it off the previous day's shortest ladder", w)
		}
		out = slices.Insert(out, k, w)
		pool = pool[1:]
	}
	if last := out[len(out)-1]; !mayFollow(last, out[0]) {
		return nil, fmt.Errorf("calendar: %q cannot wrap around to %q", last, out[0])
	}
	return out, nil
}

// shuffleCalendar returns starts sorted, then Fisher-Yates shuffled with a
// generator seeded by calendarSeed.
func shuffleCalendar(starts []string) []string {
	out := slices.Sorted(slices.Values(starts))
	rng := splitMix64(calendarSeed)
	for i := len(out) - 1; i > 0; i-- {
		j := int(rng.next() % uint64(i+1))
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// splitMix64 is a small fixed-algorithm generator, so the calendar order never
// depends on the Go version's math/rand.
type splitMix64 uint64

func (s *splitMix64) next() uint64 {
	*s += 0x9e3779b97f4a7c15
	z := uint64(*s)
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
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

// DailyNumber returns the daily puzzle number at t: 1 plus whole days since
// #1 opened at 2026-10-11 08:00 UTC. Before then it is 1, so #1 is the first
// puzzle anyone sees and there is never a #0.
func DailyNumber(t time.Time) int {
	if t.Before(dailyEpoch) {
		return 1
	}
	return int(t.Sub(dailyEpoch)/(24*time.Hour)) + 1
}

// DailyOpens returns when daily puzzle n opens, 08:00 UTC; puzzle n+1 opens
// 24 hours later.
func DailyOpens(n int) time.Time {
	return dailyEpoch.Add(time.Duration(n-1) * 24 * time.Hour)
}

// DailyStart returns the start word of daily puzzle n, calendar entry
// (n-1) mod length; the calendar wraps.
func (d *Data) DailyStart(n int) string {
	size := len(d.calendar)
	return d.calendar[((n-1)%size+size)%size]
}

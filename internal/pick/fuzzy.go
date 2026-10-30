package pick

import (
	"sort"
	"strings"
	"unicode"
)

// Match is an item that matches the query, with the rune positions that matched.
type Match struct {
	Item  string
	Pos   []int
	score int
}

// Filter keeps the items that contain the query's runes in order (case
// insensitive, spaces ignored), best first: consecutive runes, runes at the
// start of a path segment or word, and shorter items score higher. An empty
// query keeps every item in its original order.
func Filter(query string, items []string) []Match {
	q := []rune(strings.ToLower(strings.ReplaceAll(query, " ", "")))
	out := make([]Match, 0, len(items))
	for _, it := range items {
		if m, ok := match(q, it); ok {
			out = append(out, m)
		}
	}
	if len(q) > 0 {
		sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	}
	return out
}

func match(q []rune, item string) (Match, bool) {
	if len(q) == 0 {
		return Match{Item: item}, true
	}
	runes := []rune(item)
	best, found := Match{}, false
	// Greedy from each place the first rune matches; keep the best score.
	for start, r := range runes {
		if unicode.ToLower(r) != q[0] {
			continue
		}
		if m, ok := matchFrom(q, runes, start); ok && (!found || m.score > best.score) {
			best, found = m, true
		}
	}
	if !found {
		return Match{}, false
	}
	best.Item = item
	// Ties go to the shorter item, then to the earlier match (a host name before its address).
	best.score = best.score*8 - len(runes) - best.Pos[0]
	return best, true
}

func matchFrom(q, runes []rune, start int) (Match, bool) {
	var m Match
	qi, prev := 0, -2
	for i := start; i < len(runes) && qi < len(q); i++ {
		r := runes[i]
		if unicode.ToLower(r) != q[qi] {
			continue
		}
		s := 1
		if i == prev+1 {
			s += 4 // consecutive
		}
		if i == 0 || strings.ContainsRune("/-_. ", runes[i-1]) || unicode.IsDigit(r) && unicode.IsLetter(runes[i-1]) {
			s += 3 // start of a segment, a word, or a number (azure|24)
		}
		m.score += s
		m.Pos = append(m.Pos, i)
		prev = i
		qi++
	}
	return m, qi == len(q)
}

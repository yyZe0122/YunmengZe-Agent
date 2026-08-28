package modelcatalog

import (
	"strings"
)

// Lookup returns catalog limits for a model query (selection model segment or wire id).
// Miss or ambiguous match returns the 2026-08 loose defaults (1M / 128k).
func Lookup(query string) Limits {
	if lim, ok := lookup(current(), query); ok {
		return lim
	}
	return Limits{Context: DefaultContextWindow, Output: DefaultMaxOutput}
}

func lookup(c *catalog, query string) (Limits, bool) {
	if c == nil {
		return Limits{}, false
	}
	q := trimLower(query)
	if q == "" {
		return Limits{}, false
	}
	if e, ok := c.byID[q]; ok && e.limits.Context > 0 {
		return e.limits, true
	}
	if i := strings.IndexByte(q, '/'); i >= 0 {
		q = q[i+1:]
		if q == "" {
			return Limits{}, false
		}
		if e, ok := c.byID[q]; ok && e.limits.Context > 0 {
			return e.limits, true
		}
	}
	seg := lastSegment(q)
	if hits := unique(c.bySegment[seg]); len(hits) == 1 && hits[0].limits.Context > 0 {
		return hits[0].limits, true
	}
	q = seg
	if e, ok := uniquePrefix(c.bySegment, q); ok && e.limits.Context > 0 {
		return e.limits, true
	}
	folded := fold(q)
	if folded == "" {
		return Limits{}, false
	}
	if hits := unique(c.byFolded[folded]); len(hits) == 1 && hits[0].limits.Context > 0 {
		return hits[0].limits, true
	}
	if len(folded) >= MinFoldedContains {
		if e, ok := uniqueFoldedContains(c.all, folded); ok && e.limits.Context > 0 {
			return e.limits, true
		}
	}
	return Limits{}, false
}

func unique(hits []entry) []entry {
	switch len(hits) {
	case 0, 1:
		return hits
	}
	seen := map[string]entry{}
	for _, e := range hits {
		seen[e.id] = e
	}
	if len(seen) != 1 {
		return nil
	}
	for _, e := range seen {
		return []entry{e}
	}
	return nil
}

func uniquePrefix(bySegment map[string][]entry, q string) (entry, bool) {
	var found entry
	n := 0
	prefix := q + "-"
	for seg, hits := range bySegment {
		if !strings.HasPrefix(seg, prefix) {
			continue
		}
		rest := seg[len(prefix):]
		if !dateSuffix(rest) {
			continue
		}
		uniq := unique(hits)
		if len(uniq) != 1 {
			return entry{}, false
		}
		n++
		if n > 1 {
			return entry{}, false
		}
		found = uniq[0]
	}
	if n == 1 {
		return found, true
	}
	return entry{}, false
}

func dateSuffix(s string) bool {
	if len(s) < 4 {
		return false
	}
	for i := 0; i < 4; i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func uniqueFoldedContains(all []entry, folded string) (entry, bool) {
	var found entry
	n := 0
	seen := map[string]struct{}{}
	for _, e := range all {
		if e.folded == "" || !strings.Contains(e.folded, folded) {
			continue
		}
		if _, ok := seen[e.id]; ok {
			continue
		}
		seen[e.id] = struct{}{}
		n++
		if n > 1 {
			return entry{}, false
		}
		found = e
	}
	if n == 1 {
		return found, true
	}
	return entry{}, false
}

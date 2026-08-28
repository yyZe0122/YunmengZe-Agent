package modelcatalog

import (
	"bytes"
	"encoding/json"
	"sync/atomic"
)

const (
	// DefaultContextWindow is used when the user did not set contextWindow and catalog lookup misses.
	DefaultContextWindow int64 = 1_048_576
	// DefaultMaxOutput is the packing output term (and Anthropic required max_tokens) when unset.
	DefaultMaxOutput int64 = 128_000
	// MinFoldedContains is the minimum folded query length for unique-substring match.
	MinFoldedContains = 8
	CacheFilename     = "models.dev.json"
	CacheDirName      = "cache"
	maxCatalogBytes   = 2 << 20
)

// SourceURL is the models.dev provider-agnostic catalog. Tests may override.
var SourceURL = "https://models.dev/models.json"

// Limits is context length and output cap from models.dev (or defaults).
type Limits struct {
	Context int64
	Output  int64
}

type catalogModel struct {
	ID    string `json:"id"`
	Limit struct {
		Context int64 `json:"context"`
		Output  int64 `json:"output"`
	} `json:"limit"`
}

type entry struct {
	id      string
	segment string
	folded  string
	limits  Limits
}

type catalog struct {
	byID      map[string]entry
	bySegment map[string][]entry
	byFolded  map[string][]entry
	all       []entry
}

var loaded atomic.Pointer[catalog]

func current() *catalog {
	if c := loaded.Load(); c != nil {
		return c
	}
	c, err := parse(embeddedModels)
	if err != nil || c == nil {
		c = &catalog{
			byID:      map[string]entry{},
			bySegment: map[string][]entry{},
			byFolded:  map[string][]entry{},
		}
	}
	loaded.CompareAndSwap(nil, c)
	return loaded.Load()
}

func swap(c *catalog) {
	if c != nil {
		loaded.Store(c)
	}
}

func parse(raw []byte) (*catalog, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, errEmptyCatalog
	}
	var doc map[string]catalogModel
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	c := &catalog{
		byID:      make(map[string]entry, len(doc)),
		bySegment: make(map[string][]entry, len(doc)),
		byFolded:  make(map[string][]entry, len(doc)),
		all:       make([]entry, 0, len(doc)),
	}
	for key, m := range doc {
		id := trimLower(m.ID)
		if id == "" {
			id = trimLower(key)
		}
		if id == "" {
			continue
		}
		seg := lastSegment(id)
		e := entry{
			id:      id,
			segment: seg,
			folded:  fold(seg),
			limits:  Limits{Context: m.Limit.Context, Output: m.Limit.Output},
		}
		c.byID[id] = e
		c.bySegment[seg] = append(c.bySegment[seg], e)
		if e.folded != "" {
			c.byFolded[e.folded] = append(c.byFolded[e.folded], e)
		}
		c.all = append(c.all, e)
	}
	return c, nil
}

func lastSegment(id string) string {
	id = trimLower(id)
	if i := lastSlash(id); i >= 0 && i+1 < len(id) {
		return id[i+1:]
	}
	return id
}

func lastSlash(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return i
		}
	}
	return -1
}

func trimLower(s string) string {
	return toLower(trimSpace(s))
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && s[start] <= ' ' {
		start++
	}
	for end > start && s[end-1] <= ' ' {
		end--
	}
	return s[start:end]
}

func toLower(s string) string {
	need := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			need = true
			break
		}
	}
	if !need {
		return s
	}
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

func fold(s string) string {
	if s == "" {
		return ""
	}
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			b = append(b, c)
		}
	}
	return string(b)
}

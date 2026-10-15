package providerconfig

import (
	"fmt"
	"strings"
	"time"
)

// ChatConfig contains only settings used by the local agent.
type ChatConfig struct {
	Compaction    *ChatCompactionConfig        `json:"compaction,omitempty"`
	MaxIterations int                          `json:"max_iterations,omitempty"`
	Memory        *ChatMemoryConfig            `json:"memory,omitempty"`
	Commands      map[string]ChatCommandConfig `json:"commands,omitempty"`
	Web           *ChatWebConfig               `json:"web,omitempty"`
}
type ChatCompactionConfig struct {
	Enabled *bool `json:"enabled,omitempty"`
}
type ChatCommandConfig struct {
	Description string `json:"description,omitempty"`
	Template    string `json:"template"`
}
type ChatWebConfig struct {
	Search     string `json:"search,omitempty"`
	SearxNGURL string `json:"searxng_url,omitempty"`
	TavilyKey  string `json:"tavily_key,omitempty"`
}

const (
	WebSearchDDG     = "ddg"
	WebSearchSearxNG = "searxng"
	WebSearchTavily  = "tavily"
)

type ChatMemoryConfig struct {
	Enabled        *bool                    `json:"enabled,omitempty"`
	MaxInjectRunes int                      `json:"max_inject_runes,omitempty"`
	DefaultTTL     string                   `json:"default_ttl,omitempty"`
	Curator        *ChatMemoryCuratorConfig `json:"curator,omitempty"`
}
type ChatMemoryCuratorConfig struct {
	MinTurns    int    `json:"min_turns,omitempty"`
	MinInterval string `json:"min_interval,omitempty"`
	Enabled     *bool  `json:"enabled,omitempty"`
	MaxFacts    int    `json:"max_facts,omitempty"`
	TimeoutMS   int    `json:"timeout_ms,omitempty"`
}

func (c ChatConfig) CompactionEnabled() bool {
	return c.Compaction == nil || c.Compaction.Enabled == nil || *c.Compaction.Enabled
}
func (c ChatConfig) MemoryEnabled() bool {
	return c.Memory == nil || c.Memory.Enabled == nil || *c.Memory.Enabled
}
func (c ChatConfig) MemoryMaxInjectRunes() int {
	if c.Memory != nil && c.Memory.MaxInjectRunes > 0 {
		return c.Memory.MaxInjectRunes
	}
	return 2000
}
func (c ChatConfig) MemoryDefaultTTL() time.Duration {
	if c.Memory == nil {
		return 0
	}
	d, _ := time.ParseDuration(c.Memory.DefaultTTL)
	return d
}
func (c ChatConfig) MemoryCuratorEnabled() bool {
	return c.MemoryEnabled() && (c.Memory == nil || c.Memory.Curator == nil || c.Memory.Curator.Enabled == nil || *c.Memory.Curator.Enabled)
}
func (c ChatConfig) MemoryCuratorMaxFacts() int {
	if c.Memory != nil && c.Memory.Curator != nil && c.Memory.Curator.MaxFacts > 0 {
		return c.Memory.Curator.MaxFacts
	}
	return 3
}
func (c ChatConfig) MemoryCuratorTimeoutMS() int {
	if c.Memory != nil && c.Memory.Curator != nil && c.Memory.Curator.TimeoutMS > 0 {
		return c.Memory.Curator.TimeoutMS
	}
	return 15000
}
func (c ChatConfig) WebSearchBackend() string {
	if c.Web == nil || strings.TrimSpace(c.Web.Search) == "" {
		return WebSearchDDG
	}
	return strings.ToLower(strings.TrimSpace(c.Web.Search))
}
func (c ChatConfig) validate() error {
	if c.MaxIterations < 0 || c.MaxIterations > 256 {
		return fmt.Errorf("chat.max_iterations must be 0..256")
	}
	if c.Memory != nil {
		m := c.Memory
		if m.MaxInjectRunes != 0 && (m.MaxInjectRunes < 200 || m.MaxInjectRunes > 16000) {
			return fmt.Errorf("chat.memory.max_inject_runes must be 200..16000")
		}
		if m.DefaultTTL != "" {
			d, e := time.ParseDuration(m.DefaultTTL)
			if e != nil || d <= 0 || d > 8760*time.Hour {
				return fmt.Errorf("chat.memory.default_ttl must be a positive duration up to 8760h")
			}
		}
		if m.Curator != nil {
			if m.Curator.MinTurns < 0 || m.Curator.MinTurns > 64 {
				return fmt.Errorf("chat.memory.curator.min_turns must be 0..64")
			}
			if m.Curator.MinInterval != "" {
				d, e := time.ParseDuration(m.Curator.MinInterval)
				if e != nil || d < time.Minute || d > 30*24*time.Hour {
					return fmt.Errorf("chat.memory.curator.min_interval must be 1m..720h")
				}
			}
			if m.Curator.MaxFacts < 0 || m.Curator.MaxFacts > 8 {
				return fmt.Errorf("chat.memory.curator.max_facts must be 0..8")
			}
			if n := m.Curator.TimeoutMS; n != 0 && (n < 1000 || n > 120000) {
				return fmt.Errorf("chat.memory.curator.timeout_ms must be 1000..120000")
			}
		}
	}
	if err := validateChatCommands(c.Commands); err != nil {
		return err
	}
	return c.validateWeb()
}

func (c ChatConfig) MemoryCuratorMinTurns() int {
	if c.Memory != nil && c.Memory.Curator != nil && c.Memory.Curator.MinTurns > 0 {
		return c.Memory.Curator.MinTurns
	}
	return 8
}
func (c ChatConfig) MemoryCuratorMinInterval() time.Duration {
	if c.Memory != nil && c.Memory.Curator != nil && c.Memory.Curator.MinInterval != "" {
		d, _ := time.ParseDuration(c.Memory.Curator.MinInterval)
		return d
	}
	return 30 * time.Minute
}

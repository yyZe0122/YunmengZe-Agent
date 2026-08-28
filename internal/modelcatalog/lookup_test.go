package modelcatalog

import "testing"

func TestLookupExactAndFolded(t *testing.T) {
	c, err := parse([]byte(`{
  "deepseek/deepseek-v4-pro": {"id":"deepseek/deepseek-v4-pro","limit":{"context":1000000,"output":384000}},
  "deepseek/deepseek-v4-flash": {"id":"deepseek/deepseek-v4-flash","limit":{"context":1000000,"output":384000}},
  "deepseek/deepseek-v4-flash-0731": {"id":"deepseek/deepseek-v4-flash-0731","limit":{"context":1000000,"output":384000}},
  "openai/gpt-5.6-sol": {"id":"openai/gpt-5.6-sol","limit":{"context":1050000,"output":128000}},
  "anthropic/claude-sonnet-5": {"id":"anthropic/claude-sonnet-5","limit":{"context":1000000,"output":128000}}
}`))
	if err != nil {
		t.Fatal(err)
	}
	loaded.Store(c)
	t.Cleanup(func() { loaded.Store(nil) })

	tests := []struct {
		q      string
		want   int64
		wantOK bool
	}{
		{"deepseek-v4-pro", 384000, true},
		{"DeepSeekV4Pro", 384000, true},
		{"deepseekv4pro", 384000, true},
		{"deepseek/deepseek-v4-pro", 384000, true},
		{"deepseek2/deepseek/deepseek-v4-pro", 384000, true},
		{"gpt-5.6-sol", 128000, true},
		{"claude-sonnet-5", 128000, true},
		{"flash", 0, false},
		{"pro", 0, false},
		{"gpt", 0, false},
		{"unknown-model-xyz", 0, false},
		{"", 0, false},
	}
	for _, test := range tests {
		lim, ok := lookup(c, test.q)
		if ok != test.wantOK {
			t.Fatalf("%q ok=%v want %v lim=%+v", test.q, ok, test.wantOK, lim)
		}
		if test.wantOK && lim.Output != test.want {
			t.Fatalf("%q output=%d want %d", test.q, lim.Output, test.want)
		}
	}
	miss := Lookup("not-a-real-model")
	if miss.Context != DefaultContextWindow || miss.Output != DefaultMaxOutput {
		t.Fatalf("default = %+v", miss)
	}
}

func TestLookupAmbiguousDatedPrefix(t *testing.T) {
	c, err := parse([]byte(`{
  "deepseek/deepseek-v4-flash": {"id":"deepseek/deepseek-v4-flash","limit":{"context":1,"output":1}},
  "deepseek/deepseek-v4-flash-0731": {"id":"deepseek/deepseek-v4-flash-0731","limit":{"context":2,"output":2}},
  "deepseek/deepseek-v4-flash-vision-exp": {"id":"deepseek/deepseek-v4-flash-vision-exp","limit":{"context":3,"output":3}}
}`))
	if err != nil {
		t.Fatal(err)
	}
	if lim, ok := lookup(c, "deepseek-v4-flash"); !ok || lim.Context != 1 {
		t.Fatalf("exact segment should win: ok=%v lim=%+v", ok, lim)
	}
	if _, ok := lookup(c, "deepseek-v4-fla"); ok {
		t.Fatal("short prefix must miss")
	}
}

func TestLookupDatedSuffixOnly(t *testing.T) {
	c, err := parse([]byte(`{
  "lab/only-dated-0731": {"id":"lab/only-dated-0731","limit":{"context":9,"output":9}}
}`))
	if err != nil {
		t.Fatal(err)
	}
	lim, ok := lookup(c, "only-dated")
	if !ok || lim.Context != 9 {
		t.Fatalf("dated suffix lim=%+v ok=%v", lim, ok)
	}
}

func TestLookupEmbeddedDeepSeek(t *testing.T) {
	loaded.Store(nil)
	lim := Lookup("deepseek-v4-flash")
	if lim.Context < 100_000 || lim.Output < 64_000 {
		t.Fatalf("embedded lookup = %+v", lim)
	}
	chat := Lookup("deepseek-chat")
	if chat.Context < 100_000 {
		t.Fatalf("deepseek-chat = %+v", chat)
	}
}

package modelstream

import (
	"testing"
	"time"

	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

func TestHubPublishSubscribe(t *testing.T) {
	h := NewHub()
	h.Debounce = time.Millisecond
	ch, cancel := h.Subscribe("sess-1", "", 8)
	defer cancel()

	h.Publish("sess-1", "task-1", "run-1", "", providerapi.StreamEvent{
		Type: providerapi.StreamDelta, ContentDelta: "hi",
	})
	// Wait for debounce flush.
	select {
	case env := <-ch:
		if env.Event.ContentDelta != "hi" || env.SessionID != "sess-1" {
			t.Fatalf("env = %#v", env)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}

	h.Publish("other", "task-2", "run-2", "", providerapi.StreamEvent{
		Type: providerapi.StreamDelta, ContentDelta: "nope",
	})
	select {
	case env := <-ch:
		t.Fatalf("unexpected second event %#v", env)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestHubDebounceCoalescesDeltas(t *testing.T) {
	h := NewHub()
	h.Debounce = 40 * time.Millisecond
	ch, cancel := h.Subscribe("", "", 16)
	defer cancel()

	h.Publish("s", "t", "r", "", providerapi.StreamEvent{Type: providerapi.StreamDelta, ContentDelta: "a"})
	h.Publish("s", "t", "r", "", providerapi.StreamEvent{Type: providerapi.StreamDelta, ContentDelta: "b"})
	h.Publish("s", "t", "r", "", providerapi.StreamEvent{Type: providerapi.StreamDelta, ContentDelta: "c"})

	select {
	case env := <-ch:
		if env.Event.Type != providerapi.StreamDelta || env.Event.ContentDelta != "abc" {
			t.Fatalf("coalesced = %#v", env.Event)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
	select {
	case env := <-ch:
		t.Fatalf("extra event %#v", env)
	case <-time.After(30 * time.Millisecond):
	}
}

func TestHubPublishTerminalFlushesAndCompletes(t *testing.T) {
	h := NewHub()
	h.Debounce = time.Hour // would never fire without flush
	ch, cancel := h.Subscribe("", "", 16)
	defer cancel()

	h.Publish("s", "t", "r", "", providerapi.StreamEvent{Type: providerapi.StreamDelta, ContentDelta: "x"})
	h.PublishTerminal("s", "t", "r")

	var deltas, completes int
	deadline := time.After(time.Second)
	for deltas+completes < 2 {
		select {
		case env := <-ch:
			switch env.Event.Type {
			case providerapi.StreamDelta:
				if env.Event.ContentDelta != "x" {
					t.Fatalf("delta = %#v", env.Event)
				}
				deltas++
			case providerapi.StreamComplete:
				completes++
			default:
				t.Fatalf("unexpected %#v", env.Event)
			}
		case <-deadline:
			t.Fatalf("timeout deltas=%d completes=%d", deltas, completes)
		}
	}
}

func TestTeeStreamHandler(t *testing.T) {
	var got []string
	primary := func(e providerapi.StreamEvent) error {
		got = append(got, "p:"+e.ContentDelta)
		return nil
	}
	side := func(e providerapi.StreamEvent) error {
		got = append(got, "s:"+e.ContentDelta)
		return nil
	}
	h := providerapi.TeeStreamHandler(primary, side)
	if err := h(providerapi.StreamEvent{Type: providerapi.StreamDelta, ContentDelta: "x"}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "p:x" || got[1] != "s:x" {
		t.Fatalf("got %#v", got)
	}
}

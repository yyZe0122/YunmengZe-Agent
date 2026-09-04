package agent

import "testing"

func TestInboxClaimStepFIFOAndDedup(t *testing.T) {
	in := NewInbox()
	in.Steer("s1", "run-1", "a", "first")
	in.Steer("s1", "run-1", "a", "dup")
	in.Steer("s1", "run-1", "b", "second")
	in.Steer("s2", "run-2", "d", "other")

	got := in.ClaimStep("s1", "run-1")
	if len(got) != 2 || got[0].Text != "first" || got[1].Text != "second" {
		t.Fatalf("claim step = %+v", got)
	}
	if in.Pending("s1") {
		t.Fatal("s1 next-step should be empty")
	}
	if !in.Pending("s2") {
		t.Fatal("s2 must be untouched")
	}
}

func TestInboxClaimStepDoesNotCrossRuns(t *testing.T) {
	in := NewInbox()
	in.Steer("s1", "parent-run", "a", "parent steer")
	in.Steer("s1", "child-run", "b", "child steer")
	got := in.ClaimStep("s1", "child-run")
	if len(got) != 1 || got[0].Text != "child steer" {
		t.Fatalf("child claim = %+v", got)
	}
	if !in.Pending("s1") {
		t.Fatal("parent steer must remain")
	}
	parent := in.ClaimStep("s1", "parent-run")
	if len(parent) != 1 || parent[0].Text != "parent steer" {
		t.Fatalf("parent claim = %+v", parent)
	}
}

func TestInboxClearDropsSessionOnly(t *testing.T) {
	in := NewInbox()
	in.Steer("s1", "run-1", "a", "one")
	in.Steer("s2", "run-2", "b", "two")
	in.Clear("s1")
	if in.Pending("s1") {
		t.Fatal("s1 should be cleared")
	}
	if !in.Pending("s2") {
		t.Fatal("s2 must be untouched")
	}
	in.Clear("")
	if in.Pending("s2") {
		t.Fatal("empty sessionID clears all")
	}
}

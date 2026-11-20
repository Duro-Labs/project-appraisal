package model

import (
	"testing"
	"time"
)

func TestAddPromptKeepsFirstAndLatest(t *testing.T) {
	var s Session
	for i := 0; i < MaxPrompts+5; i++ {
		s.AddPrompt(string(rune('a' + i)))
	}
	if len(s.Prompts) != MaxPrompts {
		t.Fatalf("len = %d, want %d", len(s.Prompts), MaxPrompts)
	}
	if s.Prompts[0] != "a" {
		t.Errorf("first = %q, want a", s.Prompts[0])
	}
	if got, want := s.Prompts[MaxPrompts-1], string(rune('a'+MaxPrompts+4)); got != want {
		t.Errorf("last = %q, want %q", got, want)
	}
}

func TestTouchTracksBounds(t *testing.T) {
	var s Session
	t1 := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	s.Touch(t2)
	s.Touch(t1)
	s.Touch(time.Time{})
	if !s.StartedAt.Equal(t1) || !s.EndedAt.Equal(t2) {
		t.Fatalf("bounds = %v..%v", s.StartedAt, s.EndedAt)
	}
}

func TestDedupeHelpersAndTotals(t *testing.T) {
	var s Session
	s.AddFile("/a")
	s.AddFile("/a")
	s.AddFile("")
	s.AddRefs("o/r#1", "o/r#1")
	s.AddLeaf("u1")
	s.AddLeaf("u1")
	s.CountTool("Bash")
	s.CountTool("Bash")
	s.Usage = []Usage{{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4, Reasoning: 99}}
	if len(s.FilesWritten) != 1 || len(s.Refs) != 1 || len(s.LeafUUIDs) != 1 || s.Tools["Bash"] != 2 {
		t.Fatalf("dedupe failed: %+v", s)
	}
	if s.TotalTokens() != 10 {
		t.Fatalf("total = %d, want 10 (reasoning is inside output, not added)", s.TotalTokens())
	}
}

func TestValidDecision(t *testing.T) {
	ok := []Decision{
		{Kind: DecisionMerge, A: "x", B: "y"},
		{Kind: DecisionDetach, A: "x"},
		{Kind: DecisionName, A: "x", Name: "Codex support"},
		{Kind: DecisionUnnest, A: "x"},
	}
	for _, d := range ok {
		if err := ValidDecision(d); err != nil {
			t.Errorf("%+v: %v", d, err)
		}
	}
	bad := []Decision{
		{Kind: "explode", A: "x"},
		{Kind: DecisionMerge, A: "x"},
		{Kind: DecisionNest, A: "x", B: "x"},
		{Kind: DecisionDetach},
	}
	for _, d := range bad {
		if ValidDecision(d) == nil {
			t.Errorf("%+v accepted", d)
		}
	}
}

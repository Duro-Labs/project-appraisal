package card

import (
	"strings"
	"testing"
	"time"

	"appraisal/internal/model"
)

func TestBuild(t *testing.T) {
	s := model.Session{
		Provider: "claude", Cwd: "/w/repo", Branch: "feat/x", Title: "Payout fix",
		StartedAt: time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC), EndedAt: time.Date(2026, 11, 1, 12, 30, 0, 0, time.UTC),
		Prompts:      []string{"one", "two", "three", "four", "five"},
		FilesWritten: []string{"/w/repo/a.go", "/elsewhere/b.md"},
		Tools:        map[string]int{"Bash": 9, "Edit": 3, "Read": 3},
		Refs:         []string{"o/r#1"},
		LastReply:    "Merged.",
	}
	body, hash := Build(s, "repo:o/r")
	for _, want := range []string{
		"Provider: claude", "Scope: repo:o/r", "Branch: feat/x", "Title: Payout fix",
		"When: 2026-11-01 10:00 to 2026-11-01 12:30", "Refs: o/r#1",
		"- one\n- two\n- three\n- five\n", "Files written: a.go, /elsewhere/b.md",
		"Tools: Bash×9, Edit×3, Read×3", "Last reply: Merged.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("card missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "- four") {
		t.Error("card should keep the first three prompts and the last one")
	}
	if len(hash) != 16 {
		t.Errorf("hash = %q", hash)
	}
	if _, again := Build(s, "repo:o/r"); again != hash {
		t.Error("hash not stable")
	}
	empty, _ := Build(model.Session{Provider: "codex"}, "")
	if strings.Contains(empty, "Scope:") || strings.Contains(empty, "When:") || strings.Contains(empty, "Instructions") {
		t.Errorf("empty fields printed:\n%s", empty)
	}
}

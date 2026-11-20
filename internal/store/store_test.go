package store

import (
	"path/filepath"
	"testing"
	"time"

	"appraisal/internal/model"
)

func open(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func sess(id, path string, tokens int64) model.Session {
	return model.Session{
		ID: id, Provider: "claude", SourcePath: path, Cwd: "/w", Title: "t-" + id,
		StartedAt: time.Unix(1000, 0).UTC(), Prompts: []string{"p"},
		Usage: []model.Usage{{Model: "m", BillingProvider: "anthropic", Input: tokens, Calls: 1}},
	}
}

func TestReplaceFileRoundTripAndReplace(t *testing.T) {
	st := open(t)
	f := FileRow{Path: "/f1", Provider: "claude", Size: 10, Mtime: 20, ParserVersion: 1, Skipped: 2}
	if err := st.ReplaceFile(f, []model.Session{sess("claude:a", "/f1", 5), sess("claude:b", "/f1", 7)}); err != nil {
		t.Fatal(err)
	}
	same, err := st.FileUnchanged("/f1", 10, 20, 1)
	if err != nil || !same {
		t.Fatalf("FileUnchanged = %v, %v", same, err)
	}
	if same, _ := st.FileUnchanged("/f1", 10, 20, 2); same {
		t.Fatal("parser version bump must force a re-read")
	}
	f.Size = 11
	if err := st.ReplaceFile(f, []model.Session{sess("claude:a", "/f1", 9)}); err != nil {
		t.Fatal(err)
	}
	ss, err := st.Sessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "claude:a" || ss[0].TotalTokens() != 9 || ss[0].Title != "t-claude:a" {
		t.Fatalf("after replace: %+v", ss)
	}
	sk, _ := st.Skips()
	if sk["claude"] != 2 {
		t.Fatalf("skips = %v", sk)
	}
}

func TestDerivedLinksJudgmentsDecisions(t *testing.T) {
	st := open(t)
	if err := st.ReplaceFile(FileRow{Path: "/f2", Provider: "claude"}, []model.Session{sess("x", "/f2", 1)}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveDerived([]Derived{{SessionID: "x", Repo: "o/r", Scope: "repo:o/r", Card: "c", CardHash: "h"}}); err != nil {
		t.Fatal(err)
	}
	d, _ := st.Derived()
	if d["x"].CardHash != "h" {
		t.Fatalf("derived = %v", d)
	}
	if err := st.ReplaceRuleLinks([]model.Link{{A: "x", B: "y", Source: "rule:branch", Confidence: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceRuleLinks([]model.Link{{A: "x", B: "z", Source: "rule:ref", Confidence: 1}}); err != nil {
		t.Fatal(err)
	}
	links, _ := st.Links()
	if len(links) != 1 || links[0].B != "z" {
		t.Fatalf("links = %v", links)
	}
	old := model.Judgment{PairHash: "p1", A: "x", B: "y", Probability: 0.2, InputTokens: 100, JudgedAt: time.Unix(1, 0)}
	newer := model.Judgment{PairHash: "p2", A: "x", B: "y", Probability: 0.9, InputTokens: 50, JudgedAt: time.Unix(2, 0)}
	st.SaveJudgment(old)
	st.SaveJudgment(newer)
	if ok, _ := st.HasJudgment("p1"); !ok {
		t.Fatal("HasJudgment p1")
	}
	js, _ := st.Judgments()
	if len(js) != 1 || js[0].Probability != 0.9 {
		t.Fatalf("latest judgment per pair = %v", js)
	}
	calls, tok, _ := st.JudgmentStats()
	if calls != 2 || tok != 150 {
		t.Fatalf("stats = %d %d", calls, tok)
	}
	st.AddDecision(model.Decision{Kind: model.DecisionMerge, A: "x", B: "y", CreatedAt: time.Unix(5, 0)})
	st.AddDecision(model.Decision{Kind: model.DecisionName, A: "x", Name: "N", CreatedAt: time.Unix(6, 0)})
	ds, _ := st.Decisions()
	if len(ds) != 2 || ds[0].Kind != model.DecisionMerge || ds[1].Name != "N" || ds[1].ID <= ds[0].ID {
		t.Fatalf("decisions = %+v", ds)
	}
}

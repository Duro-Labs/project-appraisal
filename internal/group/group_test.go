package group

import (
	"testing"
	"time"

	"appraisal/internal/model"
)

var base = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

func s(id string, hour int, tokens int64, parent string) model.Session {
	return model.Session{ID: id, Provider: "claude", ParentID: parent, Title: "t " + id, StartedAt: base.Add(time.Duration(hour) * time.Hour),
		Usage: []model.Usage{{Model: "m", BillingProvider: "anthropic", Input: tokens}}}
}

func sessions() []model.Session {
	return []model.Session{s("a", 1, 10, ""), s("a/sub", 2, 5, "a"), s("b", 3, 20, ""), s("c", 4, 40, ""), s("d", 5, 80, "")}
}

func groupOf(gs []*Group, sid string) *Group {
	for _, g := range gs {
		for _, m := range g.Sessions {
			if m == sid {
				return g
			}
		}
	}
	return nil
}

func dec(id int64, kind, a, b, name string) model.Decision {
	return model.Decision{ID: id, Kind: kind, A: a, B: b, Name: name}
}

func TestRuleLinkAndRollup(t *testing.T) {
	gs, _ := Build(Input{Sessions: sessions(), Links: []model.Link{{A: "a/sub", B: "b", Source: "rule:ref"}}, Threshold: 0.85})
	g := groupOf(gs, "b")
	if g != groupOf(gs, "a") || g.ID != "g:a" || len(g.Roots) != 2 || len(g.Sessions) != 3 {
		t.Fatalf("group = %+v", g)
	}
	if g.Totals.Total != 35 || g.Totals.ByModel["m"] != 35 || g.Totals.ByProvider["claude"] != 35 {
		t.Fatalf("totals = %+v", g.Totals)
	}
	if gs[0].ID != "g:d" {
		t.Errorf("groups not sorted by tokens: first = %s", gs[0].ID)
	}
}

func TestJudgmentThreshold(t *testing.T) {
	in := Input{Sessions: sessions(), Threshold: 0.85, Judgments: []model.Judgment{{A: "b", B: "c", Probability: 0.9}, {A: "c", B: "d", Probability: 0.6}}}
	gs, _ := Build(in)
	if groupOf(gs, "b") != groupOf(gs, "c") || groupOf(gs, "c") == groupOf(gs, "d") {
		t.Fatal("threshold not applied")
	}
}

func TestDecisions(t *testing.T) {
	in := Input{Sessions: sessions(), Threshold: 0.85,
		Links: []model.Link{{A: "a", B: "b", Source: "rule:branch"}, {A: "b", B: "c", Source: "rule:ref"}},
		Decisions: []model.Decision{
			dec(1, model.DecisionDetach, "c", "", ""),
			dec(2, model.DecisionMerge, "c", "d", ""),
			dec(3, model.DecisionName, "d", "", "Payouts"),
			dec(4, model.DecisionNest, "a", "d", ""),
			dec(5, model.DecisionNest, "d", "a", ""),
			dec(6, model.DecisionMerge, "ghost", "a", ""),
		}}
	gs, stale := Build(in)
	ab, cd := groupOf(gs, "a"), groupOf(gs, "c")
	if ab != groupOf(gs, "b") || cd != groupOf(gs, "d") || ab == cd {
		t.Fatalf("detach/merge wrong: ab=%v cd=%v", ab.Sessions, cd.Sessions)
	}
	if cd.Name != "Payouts" {
		t.Errorf("name = %q", cd.Name)
	}
	if ab.ParentID != cd.ID || cd.ParentID != "" || len(cd.Children) != 1 {
		t.Errorf("nesting: ab.parent=%q cd.parent=%q children=%v (cycle must be ignored)", ab.ParentID, cd.ParentID, cd.Children)
	}
	if cd.TreeTotal != cd.Totals.Total+ab.TreeTotal {
		t.Errorf("tree total %d", cd.TreeTotal)
	}
	if len(stale) != 1 || stale[0].A != "ghost" {
		t.Errorf("stale = %+v", stale)
	}
	in.Decisions = append(in.Decisions, dec(7, model.DecisionUnlink, "a", "b", ""), dec(8, model.DecisionUnnest, "a", "", ""))
	gs, _ = Build(in)
	if groupOf(gs, "a") == groupOf(gs, "b") || groupOf(gs, "a").ParentID != "" {
		t.Error("unlink/unnest not applied")
	}
}

func TestDetachDropsEarlierMerge(t *testing.T) {
	in := Input{Sessions: sessions(), Decisions: []model.Decision{dec(1, model.DecisionMerge, "b", "c", ""), dec(2, model.DecisionDetach, "c", "", "")}}
	gs, _ := Build(in)
	if groupOf(gs, "b") == groupOf(gs, "c") {
		t.Fatal("detach must undo an earlier merge")
	}
}

package link

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

func has(t *testing.T, nodes []Node, source, a, b string) {
	t.Helper()
	for _, l := range Rules(nodes, "/h/Vault") {
		if l.Source == source && ((l.A == a && l.B == b) || (l.A == b && l.B == a)) {
			return
		}
	}
	t.Errorf("missing %s link %s-%s", source, a, b)
}

func none(t *testing.T, nodes []Node) {
	t.Helper()
	if ls := Rules(nodes, "/h/Vault"); len(ls) != 0 {
		t.Errorf("unexpected links %+v", ls)
	}
}

func TestRules(t *testing.T) {
	has(t, []Node{{ID: "codex:a"}, {ID: "codex:b", ContinuesID: "codex:a"}}, "rule:continues", "codex:a", "codex:b")
	none(t, []Node{{ID: "codex:b", ContinuesID: "codex:missing"}})
	has(t, []Node{{ID: "claude:a", LeafUUIDs: []string{"u9"}}, {ID: "claude:b", HeadParentUUID: "u9"}}, "rule:resume", "claude:a", "claude:b")
	has(t, []Node{{ID: "x", Refs: []string{"o/r#1"}}, {ID: "y", Refs: []string{"o/r#1"}}}, "rule:ref", "x", "y")
	has(t, []Node{{ID: "x", Repo: "o/r", Branch: "feat/a"}, {ID: "y", Repo: "o/r", Branch: "feat/a"}}, "rule:branch", "x", "y")
	none(t, []Node{{ID: "x", Repo: "o/r", Branch: "main"}, {ID: "y", Repo: "o/r", Branch: "main"}})
	none(t, []Node{{ID: "x", Repo: "o/r", Branch: "feat/a"}, {ID: "y", Repo: "o/s", Branch: "feat/a"}})
	has(t, []Node{{ID: "x", FilesWritten: []string{"/h/Vault/A/p.md"}}, {ID: "y", FilesWritten: []string{"/h/Vault/A/p.md"}}}, "rule:vault-file", "x", "y")
	none(t, []Node{{ID: "x", FilesWritten: []string{"/code/a.go"}}, {ID: "y", FilesWritten: []string{"/code/a.go"}}})
	launcher := Node{ID: "claude:l", Cwd: "/w", Launches: []time.Time{t0}}
	has(t, []Node{launcher, {ID: "hermes:default:h", Provider: "hermes", Cwd: "/w", Start: t0.Add(30 * time.Second)}}, "rule:launched", "claude:l", "hermes:default:h")
	none(t, []Node{launcher, {ID: "hermes:default:h", Provider: "hermes", Cwd: "/w", Start: t0.Add(10 * time.Minute)}})
	none(t, []Node{launcher, {ID: "hermes:default:h", Provider: "hermes", Cwd: "/other", Start: t0.Add(30 * time.Second)}})
	three := Rules([]Node{{ID: "a", Refs: []string{"o/r#1"}}, {ID: "b", Refs: []string{"o/r#1"}}, {ID: "c", Refs: []string{"o/r#1"}}}, "")
	if len(three) != 2 {
		t.Errorf("three sessions on one ref should chain with 2 links, got %d", len(three))
	}
}

func TestVaultFileRuleIsOptIn(t *testing.T) {
	nodes := []Node{
		{ID: "x", FilesWritten: []string{"/h/Vault/A/p.md"}},
		{ID: "y", FilesWritten: []string{"/h/Vault/A/p.md"}},
	}
	if ls := Rules(nodes, ""); len(ls) != 0 {
		t.Errorf("empty vaultRoot must yield no links, got %+v", ls)
	}
}

func TestCandidates(t *testing.T) {
	var nodes []Node
	for i := 0; i < 5; i++ {
		nodes = append(nodes, Node{ID: string(rune('a' + i)), Scope: "repo:o/r", Start: t0.Add(time.Duration(i) * 24 * time.Hour)})
	}
	nodes = append(nodes, Node{ID: "far", Scope: "repo:o/r", Start: t0.Add(60 * 24 * time.Hour)})
	nodes = append(nodes, Node{ID: "other", Scope: "repo:o/s", Start: t0})
	nodes = append(nodes, Node{ID: "noscope", Start: t0})
	pairs := Candidates(nodes, 14*24*time.Hour, 2)
	got := map[[2]string]bool{}
	for _, p := range pairs {
		if p[0] >= p[1] {
			t.Errorf("pair not ordered: %v", p)
		}
		got[p] = true
	}
	want := [][2]string{{"a", "b"}, {"a", "c"}, {"b", "c"}, {"b", "d"}, {"c", "d"}, {"c", "e"}, {"d", "e"}}
	if len(pairs) != len(want) {
		t.Fatalf("pairs = %v", pairs)
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing %v", w)
		}
	}
}

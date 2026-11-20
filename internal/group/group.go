package group

import (
	"sort"
	"strings"
	"time"

	"appraisal/internal/model"
)

const maxParentDepth = 32

type Input struct {
	Sessions  []model.Session
	Scopes    map[string]string
	Links     []model.Link
	Judgments []model.Judgment
	Threshold float64
	Decisions []model.Decision
}

type Totals struct {
	Input, Output, CacheRead, CacheWrite, Reasoning, Calls, Total int64
	ByModel, ByProvider                                           map[string]int64
}

type Group struct {
	ID, Name, Suggested, ParentID, Scope string
	Roots, Sessions, Children            []string
	Totals                               Totals
	TreeTotal                            int64
	Start, End                           time.Time
}

type pair [2]string

func key(a, b string) pair {
	if a > b {
		a, b = b, a
	}
	return pair{a, b}
}

func Build(in Input) ([]*Group, []model.Decision) {
	byID := make(map[string]*model.Session, len(in.Sessions))
	for i := range in.Sessions {
		byID[in.Sessions[i].ID] = &in.Sessions[i]
	}
	rootOf := func(id string) string {
		cur := id
		for i := 0; i < maxParentDepth; i++ {
			s := byID[cur]
			if s == nil || s.ParentID == "" || byID[s.ParentID] == nil {
				return cur
			}
			cur = s.ParentID
		}
		return cur
	}

	var stale []model.Decision
	var valid []model.Decision
	detached := map[string]bool{}
	blocked := map[pair]bool{}
	userEdges := map[pair]bool{}
	for _, d := range in.Decisions {
		if byID[d.A] == nil || (model.NeedsB(d.Kind) && byID[d.B] == nil) {
			stale = append(stale, d)
			continue
		}
		valid = append(valid, d)
		ra := rootOf(d.A)
		switch d.Kind {
		case model.DecisionMerge:
			userEdges[key(ra, rootOf(d.B))] = true
		case model.DecisionUnlink:
			k := key(ra, rootOf(d.B))
			blocked[k] = true
			delete(userEdges, k)
		case model.DecisionDetach:
			detached[ra] = true
			for e := range userEdges {
				if e[0] == ra || e[1] == ra {
					delete(userEdges, e)
				}
			}
		}
	}

	uf := newUnionFind()
	for id := range byID {
		uf.add(rootOf(id))
	}
	auto := func(a, b string) {
		if byID[a] == nil || byID[b] == nil {
			return
		}
		ra, rb := rootOf(a), rootOf(b)
		if ra == rb || detached[ra] || detached[rb] || blocked[key(ra, rb)] {
			return
		}
		uf.union(ra, rb)
	}
	for _, l := range in.Links {
		auto(l.A, l.B)
	}
	for _, j := range in.Judgments {
		if j.Probability >= in.Threshold {
			auto(j.A, j.B)
		}
	}
	for e := range userEdges {
		uf.union(e[0], e[1])
	}

	members := map[string][]string{}
	for id := range byID {
		c := uf.find(rootOf(id))
		members[c] = append(members[c], id)
	}
	groupOfRoot := map[string]*Group{}
	var groups []*Group
	for _, ids := range members {
		g := newGroup(ids, byID, rootOf, in.Scopes)
		groups = append(groups, g)
		for _, r := range g.Roots {
			groupOfRoot[r] = g
		}
	}
	groupOf := func(sid string) *Group { return groupOfRoot[rootOf(sid)] }

	byGroupID := map[string]*Group{}
	for _, g := range groups {
		byGroupID[g.ID] = g
	}
	for _, d := range valid {
		switch d.Kind {
		case model.DecisionName:
			groupOf(d.A).Name = strings.TrimSpace(d.Name)
		case model.DecisionNest:
			c, p := groupOf(d.A), groupOf(d.B)
			if c != p && !isAncestor(c.ID, p, byGroupID) {
				c.ParentID = p.ID
			}
		case model.DecisionUnnest:
			groupOf(d.A).ParentID = ""
		}
	}
	for _, g := range groups {
		if p := byGroupID[g.ParentID]; p != nil {
			p.Children = append(p.Children, g.ID)
		}
	}
	for _, g := range groups {
		sort.Strings(g.Children)
	}
	memo := map[string]int64{}
	for _, g := range groups {
		treeTotal(g, byGroupID, memo)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].TreeTotal != groups[j].TreeTotal {
			return groups[i].TreeTotal > groups[j].TreeTotal
		}
		return groups[i].ID < groups[j].ID
	})
	return groups, stale
}

func newGroup(ids []string, byID map[string]*model.Session, rootOf func(string) string, scopes map[string]string) *Group {
	g := &Group{Totals: Totals{ByModel: map[string]int64{}, ByProvider: map[string]int64{}}}
	sort.Slice(ids, func(i, j int) bool {
		a, b := byID[ids[i]], byID[ids[j]]
		if !a.StartedAt.Equal(b.StartedAt) {
			return a.StartedAt.Before(b.StartedAt)
		}
		return a.ID < b.ID
	})
	titles := map[string]int{}
	for _, id := range ids {
		s := byID[id]
		g.Sessions = append(g.Sessions, id)
		if rootOf(id) == id {
			g.Roots = append(g.Roots, id)
			if s.Title != "" {
				titles[s.Title]++
			}
		}
		if !s.StartedAt.IsZero() && (g.Start.IsZero() || s.StartedAt.Before(g.Start)) {
			g.Start = s.StartedAt
		}
		if s.EndedAt.After(g.End) {
			g.End = s.EndedAt
		}
		for _, u := range s.Usage {
			t := &g.Totals
			t.Input += u.Input
			t.Output += u.Output
			t.CacheRead += u.CacheRead
			t.CacheWrite += u.CacheWrite
			t.Reasoning += u.Reasoning
			t.Calls += u.Calls
			t.Total += u.Total()
			t.ByModel[u.Model] += u.Total()
			t.ByProvider[s.Provider] += u.Total()
		}
	}
	first := g.Roots[0]
	g.ID = "g:" + first
	g.Scope = scopes[first]
	g.Suggested = suggest(titles, g.Scope, byID[first])
	return g
}

func suggest(titles map[string]int, scope string, first *model.Session) string {
	best, n := "", 0
	for t, c := range titles {
		if c > n || (c == n && t < best) {
			best, n = t, c
		}
	}
	if best != "" {
		return best
	}
	if s := strings.TrimSpace(scope + " " + first.Branch); s != "" {
		return s
	}
	return first.ID
}

func isAncestor(id string, g *Group, byID map[string]*Group) bool {
	for i := 0; g != nil && i < maxParentDepth; i++ {
		if g.ID == id {
			return true
		}
		g = byID[g.ParentID]
	}
	return false
}

func treeTotal(g *Group, byID map[string]*Group, memo map[string]int64) int64 {
	if v, ok := memo[g.ID]; ok {
		return v
	}
	memo[g.ID] = g.Totals.Total
	t := g.Totals.Total
	for _, c := range g.Children {
		t += treeTotal(byID[c], byID, memo)
	}
	memo[g.ID] = t
	g.TreeTotal = t
	return t
}

type unionFind struct{ parent map[string]string }

func newUnionFind() *unionFind { return &unionFind{parent: map[string]string{}} }

func (u *unionFind) add(x string) {
	if _, ok := u.parent[x]; !ok {
		u.parent[x] = x
	}
}

func (u *unionFind) find(x string) string {
	for u.parent[x] != x {
		u.parent[x] = u.parent[u.parent[x]]
		x = u.parent[x]
	}
	return x
}

func (u *unionFind) union(a, b string) {
	u.add(a)
	u.add(b)
	ra, rb := u.find(a), u.find(b)
	if ra == rb {
		return
	}
	if ra < rb {
		u.parent[rb] = ra
	} else {
		u.parent[ra] = rb
	}
}

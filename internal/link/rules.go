package link

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	"appraisal/internal/model"
)

const launchWindow = 2 * time.Minute

var defaultBranches = map[string]bool{"": true, "main": true, "master": true, "develop": true, "dev": true, "trunk": true, "HEAD": true}

type Node struct {
	ID, Provider, Repo, Scope, Branch, Cwd string
	ContinuesID, HeadParentUUID            string
	Refs, FilesWritten, LeafUUIDs          []string
	Start                                  time.Time
	Launches                               []time.Time
}

func NodeFrom(s model.Session, repo, scope string) Node {
	return Node{ID: s.ID, Provider: s.Provider, Repo: repo, Scope: scope, Branch: s.Branch, Cwd: s.Cwd,
		ContinuesID: s.ContinuesID, HeadParentUUID: s.HeadParentUUID, Refs: s.Refs, FilesWritten: s.FilesWritten,
		LeafUUIDs: s.LeafUUIDs, Start: s.StartedAt, Launches: s.Launches}
}

func Rules(nodes []Node, vaultRoot string) []model.Link {
	var out []model.Link
	add := func(a, b, source, reason string) {
		if a == b {
			return
		}
		if a > b {
			a, b = b, a
		}
		out = append(out, model.Link{A: a, B: b, Source: source, Confidence: 1, Reason: reason})
	}
	byID := map[string]bool{}
	leaves := map[string]string{}
	for _, n := range nodes {
		byID[n.ID] = true
		for _, u := range n.LeafUUIDs {
			leaves[u] = n.ID
		}
	}
	refs, branches, vaultFiles := map[string][]string{}, map[string][]string{}, map[string][]string{}
	for _, n := range nodes {
		if n.ContinuesID != "" && byID[n.ContinuesID] {
			add(n.ContinuesID, n.ID, "rule:continues", "continues "+n.ContinuesID)
		}
		if prev, ok := leaves[n.HeadParentUUID]; ok && n.HeadParentUUID != "" {
			add(prev, n.ID, "rule:resume", "resumes "+prev)
		}
		for _, r := range n.Refs {
			refs[r] = append(refs[r], n.ID)
		}
		if n.Repo != "" && !defaultBranches[n.Branch] {
			k := n.Repo + "@" + n.Branch
			branches[k] = append(branches[k], n.ID)
		}
		for _, f := range n.FilesWritten {
			if vaultRoot != "" && strings.HasPrefix(f, vaultRoot+string(filepath.Separator)) {
				vaultFiles[f] = append(vaultFiles[f], n.ID)
			}
		}
	}
	chain := func(groups map[string][]string, source, label string) {
		keys := make([]string, 0, len(groups))
		for k := range groups {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			ids := uniqueSorted(groups[k])
			for i := 1; i < len(ids); i++ {
				add(ids[i-1], ids[i], source, label+" "+k)
			}
		}
	}
	chain(refs, "rule:ref", "same ref")
	chain(branches, "rule:branch", "same branch")
	chain(vaultFiles, "rule:vault-file", "same Vault file")
	for _, l := range nodes {
		if len(l.Launches) == 0 {
			continue
		}
		for _, h := range nodes {
			if h.Provider != "hermes" || h.Cwd != l.Cwd || h.ID == l.ID {
				continue
			}
			for _, at := range l.Launches {
				if d := h.Start.Sub(at); d >= 0 && d <= launchWindow {
					add(l.ID, h.ID, "rule:launched", "launched by "+l.ID)
					break
				}
			}
		}
	}
	return out
}

func Candidates(nodes []Node, window time.Duration, neighbours int) [][2]string {
	byScope := map[string][]Node{}
	for _, n := range nodes {
		if n.Scope != "" {
			byScope[n.Scope] = append(byScope[n.Scope], n)
		}
	}
	seen := map[[2]string]bool{}
	var out [][2]string
	scopes := make([]string, 0, len(byScope))
	for s := range byScope {
		scopes = append(scopes, s)
	}
	sort.Strings(scopes)
	for _, s := range scopes {
		list := byScope[s]
		sort.Slice(list, func(i, j int) bool {
			if !list[i].Start.Equal(list[j].Start) {
				return list[i].Start.Before(list[j].Start)
			}
			return list[i].ID < list[j].ID
		})
		for i := range list {
			for j := i + 1; j < len(list) && j <= i+neighbours; j++ {
				if list[j].Start.Sub(list[i].Start) > window {
					break
				}
				p := [2]string{list[i].ID, list[j].ID}
				if p[0] > p[1] {
					p[0], p[1] = p[1], p[0]
				}
				if !seen[p] {
					seen[p] = true
					out = append(out, p)
				}
			}
		}
	}
	return out
}

func uniqueSorted(ids []string) []string {
	sort.Strings(ids)
	out := ids[:0]
	for i, id := range ids {
		if i == 0 || id != ids[i-1] {
			out = append(out, id)
		}
	}
	return out
}

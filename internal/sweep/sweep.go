package sweep

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"appraisal/internal/card"
	"appraisal/internal/group"
	"appraisal/internal/link"
	"appraisal/internal/model"
	"appraisal/internal/scope"
	"appraisal/internal/source/claude"
	"appraisal/internal/source/codex"
	"appraisal/internal/source/hermes"
	"appraisal/internal/store"
)

type Config struct {
	ClaudeProjects, CodexHome, HermesHome, VaultRoot, Home string
	Exclude                                                []string
	Judge                                                  link.Judge
	MaxJudgments                                           int
	Threshold                                              float64
	Window                                                 time.Duration
	Neighbours                                             int
	Log                                                    io.Writer
}

type Report struct {
	FilesRead, FilesUnchanged, FileErrors int
	RuleLinks, Candidates                 int
	Judged, JudgeErrors, Pending          int
}

type parseFn func() ([]model.Session, int, error)

func Run(ctx context.Context, st *store.Store, cfg Config) (Report, error) {
	var rep Report
	if cfg.Log == nil {
		cfg.Log = io.Discard
	}
	if err := ingest(st, cfg, &rep); err != nil {
		return rep, err
	}
	sessions, err := st.Sessions()
	if err != nil {
		return rep, err
	}
	derived, nodes, roots := derive(sessions, cfg)
	if err := st.SaveDerived(derived); err != nil {
		return rep, err
	}
	rules := link.Rules(nodes, cfg.VaultRoot)
	if err := st.ReplaceRuleLinks(rules); err != nil {
		return rep, err
	}
	rep.RuleLinks = len(rules)
	return rep, judge(ctx, st, cfg, sessions, derived, roots, &rep)
}

func ingest(st *store.Store, cfg Config, rep *Report) error {
	claudeFiles, err := claude.Discover(cfg.ClaudeProjects)
	if err != nil {
		return err
	}
	for _, p := range claudeFiles {
		p := p
		fileJob(st, cfg, rep, p, "claude", claude.ParserVersion, func() ([]model.Session, int, error) {
			s, skipped, err := claude.ParseFile(p)
			return []model.Session{s}, skipped, err
		})
	}
	titles := codex.LoadTitles(cfg.CodexHome)
	codexFiles, err := codex.Discover(cfg.CodexHome)
	if err != nil {
		return err
	}
	for _, p := range codexFiles {
		p := p
		fileJob(st, cfg, rep, p, "codex", codex.ParserVersion, func() ([]model.Session, int, error) {
			s, skipped, err := codex.ParseFile(p, titles)
			if errors.Is(err, codex.ErrNoMeta) {
				return nil, skipped, nil
			}
			return []model.Session{s}, skipped, err
		})
	}
	for _, db := range hermes.Discover(cfg.HermesHome) {
		db := db
		size, mtime := hermes.Stamp(db.Path)
		stamped(st, cfg, rep, db.Path, "hermes", hermes.ParserVersion, size, mtime, func() ([]model.Session, int, error) {
			ss, err := hermes.Read(db)
			return ss, 0, err
		})
	}
	return nil
}

func fileJob(st *store.Store, cfg Config, rep *Report, path, provider string, pv int, parse parseFn) {
	fi, err := os.Stat(path)
	if err != nil {
		rep.FileErrors++
		return
	}
	stamped(st, cfg, rep, path, provider, pv, fi.Size(), fi.ModTime().UnixNano(), parse)
}

func stamped(st *store.Store, cfg Config, rep *Report, path, provider string, pv int, size, mtime int64, parse parseFn) {
	same, err := st.FileUnchanged(path, size, mtime, pv)
	if err == nil && same {
		rep.FilesUnchanged++
		return
	}
	ss, skipped, err := parse()
	if err != nil {
		rep.FileErrors++
		fmt.Fprintf(cfg.Log, "skip %s: %v\n", path, err)
		return
	}
	ss = excluded(ss, cfg.Exclude)
	if err := st.ReplaceFile(store.FileRow{Path: path, Provider: provider, Size: size, Mtime: mtime, ParserVersion: pv, Skipped: skipped}, ss); err != nil {
		rep.FileErrors++
		fmt.Fprintf(cfg.Log, "store %s: %v\n", path, err)
		return
	}
	rep.FilesRead++
}

func excluded(ss []model.Session, prefixes []string) []model.Session {
	out := ss[:0]
	for _, s := range ss {
		drop := false
		for _, p := range prefixes {
			if p != "" && (s.Cwd == p || strings.HasPrefix(s.Cwd, p+"/")) {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, s)
		}
	}
	return out
}

func derive(sessions []model.Session, cfg Config) ([]store.Derived, []link.Node, []link.Node) {
	res := scope.NewResolver(cfg.Home, cfg.VaultRoot)
	repos := make(map[string]string, len(sessions))
	for _, s := range sessions {
		repos[s.ID] = res.Repo(s.Cwd)
	}
	scope.ResolveGittrees(repos)
	ids := map[string]bool{}
	for _, s := range sessions {
		ids[s.ID] = true
	}
	var derived []store.Derived
	var nodes, roots []link.Node
	for _, s := range sessions {
		vault := scope.VaultSubject(cfg.VaultRoot, s.Cwd, s.FilesWritten)
		key := scope.Key(s, repos[s.ID], vault)
		body, hash := card.Build(s, key)
		derived = append(derived, store.Derived{SessionID: s.ID, Repo: repos[s.ID], Scope: key, Card: body, CardHash: hash})
		n := link.NodeFrom(s, repos[s.ID], key)
		nodes = append(nodes, n)
		if s.ParentID == "" || !ids[s.ParentID] {
			roots = append(roots, n)
		}
	}
	return derived, nodes, roots
}

func judge(ctx context.Context, st *store.Store, cfg Config, sessions []model.Session, derived []store.Derived, roots []link.Node, rep *Report) error {
	pairs := link.Candidates(roots, cfg.Window, cfg.Neighbours)
	rep.Candidates = len(pairs)
	links, err := st.Links()
	if err != nil {
		return err
	}
	decisions, err := st.Decisions()
	if err != nil {
		return err
	}
	current, _ := group.Build(group.Input{Sessions: sessions, Links: links, Decisions: decisions, Threshold: cfg.Threshold})
	groupOf := map[string]string{}
	for _, g := range current {
		for _, id := range g.Sessions {
			groupOf[id] = g.ID
		}
	}
	cards := map[string]store.Derived{}
	for _, d := range derived {
		cards[d.SessionID] = d
	}
	for _, p := range pairs {
		if groupOf[p[0]] == groupOf[p[1]] {
			continue
		}
		a, b := cards[p[0]], cards[p[1]]
		h := link.PairHash(a.CardHash, b.CardHash)
		done, err := st.HasJudgment(h)
		if err != nil {
			return err
		}
		if done {
			continue
		}
		if cfg.Judge == nil || rep.Judged >= cfg.MaxJudgments {
			rep.Pending++
			continue
		}
		v, err := cfg.Judge.Same(ctx, a.Card, b.Card)
		if err != nil {
			rep.JudgeErrors++
			fmt.Fprintf(cfg.Log, "judge %s %s: %v\n", p[0], p[1], err)
			continue
		}
		rep.Judged++
		if err := st.SaveJudgment(model.Judgment{PairHash: h, A: p[0], B: p[1], Probability: v.Probability,
			InputTokens: v.InputTokens, JudgedAt: time.Now()}); err != nil {
			return err
		}
	}
	return nil
}

func Load(st *store.Store, threshold float64) (group.Input, map[string]store.Derived, error) {
	sessions, err := st.Sessions()
	if err != nil {
		return group.Input{}, nil, err
	}
	derived, err := st.Derived()
	if err != nil {
		return group.Input{}, nil, err
	}
	links, err := st.Links()
	if err != nil {
		return group.Input{}, nil, err
	}
	judgments, err := st.Judgments()
	if err != nil {
		return group.Input{}, nil, err
	}
	decisions, err := st.Decisions()
	if err != nil {
		return group.Input{}, nil, err
	}
	scopes := make(map[string]string, len(derived))
	for id, d := range derived {
		scopes[id] = d.Scope
	}
	return group.Input{Sessions: sessions, Scopes: scopes, Links: links, Judgments: judgments, Threshold: threshold, Decisions: decisions}, derived, nil
}

func Status(st *store.Store, threshold float64, w io.Writer) error {
	in, _, err := Load(st, threshold)
	if err != nil {
		return err
	}
	count, tokens := map[string]int{}, map[string]int64{}
	for _, s := range in.Sessions {
		count[s.Provider]++
		tokens[s.Provider] += s.TotalTokens()
	}
	skips, err := st.Skips()
	if err != nil {
		return err
	}
	providers := make([]string, 0, len(count))
	for p := range count {
		providers = append(providers, p)
	}
	sort.Strings(providers)
	fmt.Fprintln(w, "sessions")
	for _, p := range providers {
		fmt.Fprintf(w, "  %-7s %6d sessions  %15d tokens  %d malformed lines skipped\n", p, count[p], tokens[p], skips[p])
	}
	groups, stale := group.Build(in)
	var all, single int64
	singles := 0
	for _, g := range groups {
		all += g.Totals.Total
		if len(g.Roots) == 1 {
			singles++
			single += g.Totals.Total
		}
	}
	share := 0.0
	if all > 0 {
		share = 100 * float64(single) / float64(all)
	}
	fmt.Fprintf(w, "groups   %d (%d single-session, holding %.1f%% of tokens)\n", len(groups), singles, share)
	calls, jtok, err := st.JudgmentStats()
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "JEV      %d judgments, %d input tokens (appraisal overhead)\n", calls, jtok)
	fmt.Fprintf(w, "decisions %d, %d stale\n", len(in.Decisions), len(stale))
	return nil
}

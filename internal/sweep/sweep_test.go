package sweep

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"appraisal/internal/group"
	"appraisal/internal/link"
	"appraisal/internal/store"
)

type fakeJudge struct{ calls int }

func (f *fakeJudge) Same(ctx context.Context, a, b string) (link.Verdict, error) {
	f.calls++
	return link.Verdict{Probability: 0.95, InputTokens: 100}, nil
}

func claudeLine(cwd, branch, ts, req, prompt string) string {
	return `{"type":"user","cwd":"` + cwd + `","gitBranch":"` + branch + `","timestamp":"` + ts + `","uuid":"u-` + req + `","message":{"role":"user","content":"` + prompt + `"}}` + "\n" +
		`{"type":"assistant","timestamp":"` + ts + `","requestId":"` + req + `","uuid":"a-` + req + `","message":{"model":"claude-opus-5-5","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":10,"output_tokens":1,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}` + "\n"
}

func setup(t *testing.T) (Config, *store.Store) {
	t.Helper()
	root := t.TempDir()
	projects := filepath.Join(root, "claude", "projects")
	wt := "/nowhere/gittrees/demo/feat-x"
	files := map[string]string{
		"p/s1.jsonl": claudeLine(wt, "feat-x", "2026-11-01T10:00:00Z", "r1", "Start feature X"),
		"p/s2.jsonl": claudeLine(wt, "feat-x", "2026-11-02T10:00:00Z", "r2", "Continue feature X"),
		"p/s3.jsonl": claudeLine("/nowhere/other", "", "2026-11-03T10:00:00Z", "r3", "Something else"),
		"p/s4.jsonl": claudeLine("/nowhere/other", "", "2026-11-04T10:00:00Z", "r4", "Something else again"),
		"q/s5.jsonl": claudeLine(filepath.Join(root, ".claude-sessions-board", "summarizer"), "", "2026-11-04T11:00:00Z", "r5", "summarize"),
	}
	for rel, body := range files {
		p := filepath.Join(projects, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	st, err := store.Open(filepath.Join(root, "data", "appraisal.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return Config{
		ClaudeProjects: projects, CodexHome: filepath.Join(root, "codex"), HermesHome: filepath.Join(root, "hermes"),
		VaultRoot: filepath.Join(root, "Vault"), Home: root, Exclude: []string{filepath.Join(root, ".claude-sessions-board")},
		MaxJudgments: 10, Threshold: 0.85, Window: 14 * 24 * time.Hour, Neighbours: 8, Log: &bytes.Buffer{},
	}, st
}

func TestRunLinksCachesAndExcludes(t *testing.T) {
	cfg, st := setup(t)
	j := &fakeJudge{}
	cfg.Judge = j
	rep, err := Run(context.Background(), st, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if rep.FilesRead != 5 || rep.RuleLinks != 1 {
		t.Fatalf("report = %+v", rep)
	}
	if j.calls != 1 {
		t.Fatalf("judge calls = %d, want 1 (s1-s2 already rule-linked, only s3-s4 judged)", j.calls)
	}
	in, _, err := Load(st, cfg.Threshold)
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Sessions) != 4 {
		t.Fatalf("excluded summarizer session still stored: %d sessions", len(in.Sessions))
	}
	gs, _ := group.Build(in)
	if len(gs) != 2 {
		t.Fatalf("groups = %d, want 2", len(gs))
	}
	rep2, err := Run(context.Background(), st, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if rep2.FilesUnchanged != 5 || rep2.FilesRead != 0 || j.calls != 1 {
		t.Fatalf("second run should reuse files and judgments: %+v calls=%d", rep2, j.calls)
	}
	var out bytes.Buffer
	if err := Status(st, cfg.Threshold, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"claude", "groups", "JEV"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("status missing %q:\n%s", want, out.String())
		}
	}
}

func TestRunWithoutJudgeCountsPending(t *testing.T) {
	cfg, st := setup(t)
	rep, err := Run(context.Background(), st, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pending != 1 || rep.Judged != 0 {
		t.Fatalf("report = %+v", rep)
	}
}

type errJudge struct{ calls int }

func (f *errJudge) Same(ctx context.Context, a, b string) (link.Verdict, error) {
	f.calls++
	return link.Verdict{}, errors.New("boom")
}

func twoPairs(t *testing.T) (Config, *store.Store) {
	t.Helper()
	cfg, st := setup(t)
	projects := cfg.ClaudeProjects
	os.Remove(filepath.Join(projects, "p/s4.jsonl"))
	for rel, body := range map[string]string{
		"p/s6.jsonl": claudeLine("/nowhere/other", "", "2026-11-10T10:00:00Z", "r6", "Third thing"),
		"p/s7.jsonl": claudeLine("/nowhere/other", "", "2026-11-20T10:00:00Z", "r7", "Fourth thing"),
	} {
		os.WriteFile(filepath.Join(projects, rel), []byte(body), 0o644)
	}
	return cfg, st
}

func TestJudgeBudget(t *testing.T) {
	cfg, st := twoPairs(t)
	j := &fakeJudge{}
	cfg.Judge, cfg.MaxJudgments = j, 1
	rep, err := Run(context.Background(), st, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if j.calls != 1 || rep.Judged != 1 || rep.Pending != 1 {
		t.Fatalf("calls=%d report=%+v, want 1 call, Judged 1, Pending 1", j.calls, rep)
	}
}

func TestJudgeErrorsCountAgainstBudget(t *testing.T) {
	cfg, st := twoPairs(t)
	j := &errJudge{}
	cfg.Judge, cfg.MaxJudgments = j, 1
	rep, err := Run(context.Background(), st, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if j.calls != 1 || rep.JudgeErrors != 1 || rep.Judged != 0 || rep.Pending != 1 {
		t.Fatalf("calls=%d report=%+v, want 1 call, JudgeErrors 1, Pending 1", j.calls, rep)
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	cfg, st := twoPairs(t)
	j := &fakeJudge{}
	cfg.Judge = j
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Run(ctx, st, cfg)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if j.calls != 0 {
		t.Fatalf("judge calls = %d, want 0", j.calls)
	}
}

type cancelJudge struct {
	cancel context.CancelFunc
	calls  int
}

func (f *cancelJudge) Same(ctx context.Context, a, b string) (link.Verdict, error) {
	f.calls++
	f.cancel()
	return link.Verdict{}, ctx.Err()
}

func TestRunStopsOnInFlightCancel(t *testing.T) {
	cfg, st := twoPairs(t)
	ctx, cancel := context.WithCancel(context.Background())
	j := &cancelJudge{cancel: cancel}
	cfg.Judge, cfg.MaxJudgments = j, 10
	rep, err := Run(ctx, st, cfg)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if j.calls != 1 || rep.JudgeErrors != 0 {
		t.Fatalf("calls=%d report=%+v, want 1 call and JudgeErrors 0", j.calls, rep)
	}
}

func TestArchiveRootsAddSessionsWithoutStealingLiveOnes(t *testing.T) {
	cfg, st := setup(t)
	archive := filepath.Join(cfg.Home, "archive", "projects")
	for rel, body := range map[string]string{
		"old/s1.jsonl":   claudeLine("/nowhere/stale", "", "2026-10-01T10:00:00Z", "r1", "stale copy of s1"),
		"old/gone.jsonl": claudeLine("/nowhere/gone", "", "2026-10-02T10:00:00Z", "rg", "only in the archive"),
	} {
		p := filepath.Join(archive, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	cfg.ClaudeArchives = []string{archive}
	if _, err := Run(context.Background(), st, cfg); err != nil {
		t.Fatal(err)
	}
	in, _, err := Load(st, cfg.Threshold)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]string{}
	for _, s := range in.Sessions {
		byID[s.ID] = s.Cwd
	}
	if _, ok := byID["claude:gone"]; !ok {
		t.Fatal("archive-only session was not ingested")
	}
	if byID["claude:s1"] != "/nowhere/gittrees/demo/feat-x" {
		t.Fatalf("archive copy replaced the live session: cwd = %q", byID["claude:s1"])
	}
}

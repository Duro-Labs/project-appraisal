package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const mainLog = `{"type":"user","sessionId":"s1","cwd":"/work/repo","gitBranch":"feat/x","timestamp":"2026-11-01T10:00:00Z","uuid":"u1","parentUuid":"prev-leaf","message":{"role":"user","content":"<pasted_content id=\"a\">Fix the payout bug in Duro-Labs/demo#7</pasted_content>"}}
{"type":"user","sessionId":"s1","timestamp":"2026-11-01T10:00:01Z","isMeta":true,"uuid":"u2","parentUuid":"u1","message":{"role":"user","content":"<local-command-caveat>x</local-command-caveat>"}}
{"type":"assistant","sessionId":"s1","timestamp":"2026-11-01T10:00:05Z","requestId":"r1","uuid":"u3","parentUuid":"u1","message":{"model":"claude-opus-5-5","content":[{"type":"thinking","thinking":"..."}],"usage":{"input_tokens":2,"output_tokens":10,"cache_read_input_tokens":100,"cache_creation_input_tokens":50}}}
{"type":"assistant","sessionId":"s1","timestamp":"2026-11-01T10:00:06Z","requestId":"r1","uuid":"u4","parentUuid":"u3","message":{"model":"claude-opus-5-5","content":[{"type":"tool_use","name":"Write","input":{"file_path":"/work/repo/a.go","content":"x"}}],"usage":{"input_tokens":2,"output_tokens":10,"cache_read_input_tokens":100,"cache_creation_input_tokens":50}}}
{"type":"assistant","sessionId":"s1","timestamp":"2026-11-01T10:01:00Z","requestId":"r2","uuid":"u5","parentUuid":"u4","message":{"model":"claude-opus-5-5","content":[{"type":"tool_use","name":"Bash","input":{"command":"offload \"sort the list\""}},{"type":"text","text":"Opened https://github.com/Duro-Labs/demo/pull/8"}],"usage":{"input_tokens":1,"output_tokens":5,"cache_read_input_tokens":200,"cache_creation_input_tokens":0}}}
{"type":"pr-link","sessionId":"s1","prNumber":8,"prRepository":"Duro-Labs/demo","timestamp":"2026-11-01T10:02:00Z"}
{"type":"ai-title","aiTitle":"Payout bug fix","sessionId":"s1"}
{"type":"last-prompt","lastPrompt":"x","leafUuid":"u5","sessionId":"s1"}
not json
`

const subLog = `{"type":"user","sessionId":"s1","agentId":"abc","isSidechain":true,"cwd":"/work/repo","timestamp":"2026-11-01T10:00:30Z","uuid":"x1","parentUuid":null,"message":{"role":"user","content":"Review a.go"}}
{"type":"assistant","sessionId":"s1","agentId":"abc","timestamp":"2026-11-01T10:00:40Z","requestId":"r9","uuid":"x2","parentUuid":"x1","message":{"model":"claude-sonnet-5-5","content":[{"type":"text","text":"Looks fine."}],"usage":{"input_tokens":3,"output_tokens":4,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}
`

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseMainSession(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "-work-repo", "s1.jsonl")
	write(t, p, mainLog)
	s, skipped, err := ParseFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "claude:s1" || s.ParentID != "" || s.Provider != "claude" {
		t.Fatalf("ids: %+v", s)
	}
	if skipped != 1 {
		t.Errorf("skipped = %d, want 1", skipped)
	}
	if s.Cwd != "/work/repo" || s.Branch != "feat/x" || s.Title != "Payout bug fix" {
		t.Errorf("meta: cwd=%q branch=%q title=%q", s.Cwd, s.Branch, s.Title)
	}
	if len(s.Prompts) != 1 || !strings.HasPrefix(s.Prompts[0], "Fix the payout bug") {
		t.Errorf("prompts = %q", s.Prompts)
	}
	if len(s.Usage) != 1 {
		t.Fatalf("usage rows = %d", len(s.Usage))
	}
	u := s.Usage[0]
	if u.Calls != 2 || u.Input != 3 || u.Output != 15 || u.CacheRead != 300 || u.CacheWrite != 50 {
		t.Errorf("usage (r1 counted once) = %+v", u)
	}
	if len(s.FilesWritten) != 1 || s.FilesWritten[0] != "/work/repo/a.go" {
		t.Errorf("files = %v", s.FilesWritten)
	}
	if len(s.Launches) != 1 {
		t.Errorf("launches = %v", s.Launches)
	}
	if strings.Join(s.Refs, ",") != "duro-labs/demo#7,duro-labs/demo#8" {
		t.Errorf("refs = %v", s.Refs)
	}
	if s.HeadParentUUID != "prev-leaf" || len(s.LeafUUIDs) != 1 || s.LeafUUIDs[0] != "u5" {
		t.Errorf("uuids head=%q leaves=%v", s.HeadParentUUID, s.LeafUUIDs)
	}
	if s.Tools["Bash"] != 1 || s.Tools["Write"] != 1 {
		t.Errorf("tools = %v", s.Tools)
	}
}

func TestParseSubagentAndDiscover(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "-work-repo", "s1.jsonl"), mainLog)
	sub := filepath.Join(dir, "-work-repo", "s1", "subagents", "agent-abc.jsonl")
	write(t, sub, subLog)
	write(t, filepath.Join(dir, "-work-repo", "s1", "subagents", "agent-abc.meta.json"), "{}")
	s, _, err := ParseFile(sub)
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "claude:s1/agent-abc" || s.ParentID != "claude:s1" || s.HeadParentUUID != "" {
		t.Fatalf("subagent ids: %+v", s)
	}
	if s.TotalTokens() != 7 || s.Usage[0].Model != "claude-sonnet-5-5" {
		t.Errorf("subagent usage = %+v", s.Usage)
	}
	files, err := Discover(dir)
	if err != nil || len(files) != 2 {
		t.Fatalf("Discover = %v, %v", files, err)
	}
	if none, err := Discover(filepath.Join(dir, "missing")); err != nil || len(none) != 0 {
		t.Fatalf("missing dir: %v %v", none, err)
	}
}

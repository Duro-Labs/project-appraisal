package codex

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const rollout = `{"timestamp":"2026-11-02T09:00:00Z","type":"session_meta","payload":{"id":"t1","cwd":"/work/repo","source":"cli","thread_source":"user","forked_from_id":"t0","git":{"branch":"feat/y"}}}
{"timestamp":"2026-11-02T09:00:01Z","type":"turn_context","payload":{"cwd":"/work/repo","model":"gpt-6-sol"}}
{"timestamp":"2026-11-02T09:00:02Z","type":"event_msg","payload":{"type":"user_message","message":"Add Codex support, see Duro-Labs/demo#3"}}
{"timestamp":"2026-11-02T09:00:03Z","type":"event_msg","payload":{"type":"user_message","message":"<environment_context>x</environment_context>"}}
{"timestamp":"2026-11-02T09:00:04Z","type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","input":"*** Begin Patch\n*** Update File: src/a.go\n@@\n*** Add File: /abs/b.go\n*** End Patch"}}
{"timestamp":"2026-11-02T09:00:05Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"offload \\\"tidy\\\"\"}"}}
{"timestamp":"2026-11-02T09:00:06Z","type":"token_usage_record","payload":{"response_id":"r1","usage":{"input_tokens":100,"cached_input_tokens":40,"cache_write_input_tokens":0,"output_tokens":10,"reasoning_output_tokens":4}}}
{"timestamp":"2026-11-02T09:00:06Z","type":"token_usage_record","payload":{"response_id":"r1","usage":{"input_tokens":100,"cached_input_tokens":40,"cache_write_input_tokens":0,"output_tokens":10,"reasoning_output_tokens":4}}}
{"timestamp":"2026-11-02T09:00:07Z","type":"token_usage_record","payload":{"response_id":"r2","usage":{"input_tokens":50,"cached_input_tokens":0,"cache_write_input_tokens":5,"output_tokens":5,"reasoning_output_tokens":0}}}
{"timestamp":"2026-11-02T09:00:08Z","type":"event_msg","payload":{"type":"agent_message","message":"Done, opened https://github.com/Duro-Labs/demo/pull/4"}}
garbage
`

const subRollout = `{"timestamp":"2026-11-02T09:10:00Z","type":"session_meta","payload":{"id":"t2","cwd":"/work/repo","source":{"subagent":{"thread_spawn":{"parent_thread_id":"t1","depth":1}}},"thread_source":"subagent","forked_from_id":"t1"}}
{"timestamp":"2026-11-02T09:10:01Z","type":"turn_context","payload":{"model":"gpt-6-luna"}}
{"timestamp":"2026-11-02T09:10:02Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":30,"cached_input_tokens":10,"cache_write_input_tokens":0,"output_tokens":3,"reasoning_output_tokens":1}}}}
{"timestamp":"2026-11-02T09:10:03Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":60,"cached_input_tokens":20,"cache_write_input_tokens":0,"output_tokens":6,"reasoning_output_tokens":2}}}}
`

func write(t *testing.T, path, body string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseRollout(t *testing.T) {
	home := t.TempDir()
	p := filepath.Join(home, "sessions", "2026", "11", "02", "rollout-2026-11-02T09-00-00-t1.jsonl")
	write(t, p, rollout)
	write(t, filepath.Join(home, "session_index.jsonl"), `{"id":"t1","thread_name":"old"}`+"\n"+`{"id":"t1","thread_name":"Codex support"}`+"\n")
	s, skipped, err := ParseFile(p, LoadTitles(home))
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "codex:t1" || s.ContinuesID != "codex:t0" || s.ParentID != "" || s.Branch != "feat/y" || s.Title != "Codex support" {
		t.Fatalf("meta: %+v", s)
	}
	if skipped != 1 {
		t.Errorf("skipped = %d", skipped)
	}
	if len(s.Prompts) != 1 || len(s.Launches) != 1 {
		t.Errorf("prompts=%v launches=%v", s.Prompts, s.Launches)
	}
	if len(s.FilesWritten) != 2 || s.FilesWritten[0] != "/work/repo/src/a.go" || s.FilesWritten[1] != "/abs/b.go" {
		t.Errorf("files = %v", s.FilesWritten)
	}
	if len(s.Refs) != 2 {
		t.Errorf("refs = %v", s.Refs)
	}
	if len(s.Usage) != 1 {
		t.Fatalf("usage = %+v", s.Usage)
	}
	u := s.Usage[0]
	if u.Model != "gpt-6-sol" || u.Calls != 2 || u.Input != 110 || u.CacheRead != 40 || u.CacheWrite != 5 || u.Output != 15 || u.Reasoning != 4 {
		t.Errorf("usage (r1 once, input minus cached) = %+v", u)
	}
}

func TestSubagentFallbackAndNoMeta(t *testing.T) {
	home := t.TempDir()
	p := filepath.Join(home, "sessions", "x", "rollout-t2.jsonl")
	write(t, p, subRollout)
	s, _, err := ParseFile(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.ParentID != "codex:t1" || s.ContinuesID != "" {
		t.Fatalf("parent=%q continues=%q", s.ParentID, s.ContinuesID)
	}
	if len(s.Usage) != 1 || s.Usage[0].Input != 40 || s.Usage[0].CacheRead != 20 || s.Usage[0].Output != 6 {
		t.Fatalf("fallback usage uses last cumulative total: %+v", s.Usage)
	}
	empty := filepath.Join(home, "archived_sessions", "rollout-empty.jsonl")
	write(t, empty, `{"type":"turn_context","payload":{}}`+"\n")
	if _, _, err := ParseFile(empty, nil); !errors.Is(err, ErrNoMeta) {
		t.Fatalf("err = %v, want ErrNoMeta", err)
	}
	files, _ := Discover(home)
	if len(files) != 2 {
		t.Fatalf("Discover = %v", files)
	}
}

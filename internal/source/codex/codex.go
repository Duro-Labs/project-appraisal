package codex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"appraisal/internal/model"
	"appraisal/internal/signals"
)

const ParserVersion = 1

var ErrNoMeta = errors.New("codex: rollout has no session_meta")

type entry struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type tokens struct {
	Input      int64 `json:"input_tokens"`
	Cached     int64 `json:"cached_input_tokens"`
	CacheWrite int64 `json:"cache_write_input_tokens"`
	Output     int64 `json:"output_tokens"`
	Reasoning  int64 `json:"reasoning_output_tokens"`
}

type meta struct {
	ID           string          `json:"id"`
	Cwd          string          `json:"cwd"`
	Source       json.RawMessage `json:"source"`
	ForkedFromID string          `json:"forked_from_id"`
	Git          *struct {
		Branch string `json:"branch"`
	} `json:"git"`
}

type payload struct {
	Type       string          `json:"type"`
	Message    json.RawMessage `json:"message"`
	Name       string          `json:"name"`
	Arguments  string          `json:"arguments"`
	Input      json.RawMessage `json:"input"`
	Model      string          `json:"model"`
	Cwd        string          `json:"cwd"`
	ResponseID string          `json:"response_id"`
	Usage      *tokens         `json:"usage"`
	Info       *struct {
		Total *tokens `json:"total_token_usage"`
	} `json:"info"`
}

type parser struct {
	s          model.Session
	titles     map[string]string
	model      string
	byResp     map[string]model.Usage
	order      []string
	lastTotal  *tokens
	totalModel string
	skipped    int
}

func Discover(home string) ([]string, error) {
	var out []string
	for _, sub := range []string{"sessions", "archived_sessions"} {
		filepath.WalkDir(filepath.Join(home, sub), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if !d.IsDir() && strings.HasPrefix(d.Name(), "rollout-") && strings.HasSuffix(p, ".jsonl") {
				out = append(out, p)
			}
			return nil
		})
	}
	return out, nil
}

func LoadTitles(home string) map[string]string {
	out := map[string]string{}
	data, err := os.ReadFile(filepath.Join(home, "session_index.jsonl"))
	if err != nil {
		return out
	}
	for _, line := range bytes.Split(data, []byte("\n")) {
		var v struct {
			ID         string `json:"id"`
			ThreadName string `json:"thread_name"`
		}
		if json.Unmarshal(line, &v) == nil && v.ID != "" && v.ThreadName != "" {
			out[v.ID] = v.ThreadName
		}
	}
	return out
}

func ParseFile(path string, titles map[string]string) (model.Session, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return model.Session{}, 0, err
	}
	defer f.Close()
	p := &parser{s: model.Session{Provider: "codex", SourcePath: path}, titles: titles, byResp: map[string]model.Usage{}}
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var e entry
			if json.Unmarshal(line, &e) != nil {
				p.skipped++
			} else {
				p.apply(e)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return p.s, p.skipped, err
		}
	}
	if p.s.ID == "" {
		return p.s, p.skipped, ErrNoMeta
	}
	p.finishUsage()
	return p.s, p.skipped, nil
}

func (p *parser) apply(e entry) {
	ts, _ := time.Parse(time.RFC3339Nano, e.Timestamp)
	p.s.Touch(ts)
	if e.Type == "session_meta" {
		var m meta
		if json.Unmarshal(e.Payload, &m) == nil {
			p.meta(m)
		}
		return
	}
	var pl payload
	if json.Unmarshal(e.Payload, &pl) != nil {
		p.skipped++
		return
	}
	switch e.Type {
	case "turn_context":
		if pl.Model != "" {
			p.model = pl.Model
		}
		if pl.Cwd != "" && p.s.Cwd == "" {
			p.s.Cwd = pl.Cwd
		}
	case "token_usage_record":
		if pl.Usage != nil && pl.ResponseID != "" {
			if _, seen := p.byResp[pl.ResponseID]; !seen {
				p.order = append(p.order, pl.ResponseID)
			}
			p.byResp[pl.ResponseID] = toUsage(p.model, *pl.Usage)
		}
	case "event_msg":
		p.event(pl)
	case "response_item":
		p.item(pl, ts)
	}
}

func (p *parser) meta(m meta) {
	p.s.ID = "codex:" + m.ID
	p.s.Cwd = m.Cwd
	if m.Git != nil {
		p.s.Branch = m.Git.Branch
	}
	if parent := parentThread(m.Source); parent != "" {
		p.s.ParentID = "codex:" + parent
	} else if m.ForkedFromID != "" {
		p.s.ContinuesID = "codex:" + m.ForkedFromID
	}
	p.s.Title = p.titles[m.ID]
}

func (p *parser) event(pl payload) {
	switch pl.Type {
	case "user_message":
		text := signals.CleanPrompt(str(pl.Message))
		if !signals.IsNoisePrompt(text) {
			p.s.AddRefs(signals.Refs(text)...)
			p.s.AddPrompt(signals.Clip(text, 400))
		}
	case "agent_message":
		if t := strings.TrimSpace(str(pl.Message)); t != "" {
			p.s.LastReply = signals.Clip(t, 400)
			p.s.AddRefs(signals.Refs(t)...)
		}
	case "token_count":
		if pl.Info != nil && pl.Info.Total != nil {
			p.lastTotal = pl.Info.Total
			p.totalModel = p.model
		}
	}
}

func (p *parser) item(pl payload, ts time.Time) {
	switch pl.Type {
	case "function_call":
		p.s.CountTool(pl.Name)
		if signals.IsLaunchCommand(commandOf(pl.Arguments)) {
			p.s.Launches = append(p.s.Launches, ts)
		}
	case "custom_tool_call":
		p.s.CountTool(pl.Name)
		in := str(pl.Input)
		if pl.Name == "apply_patch" {
			for _, f := range patchFiles(in) {
				p.s.AddFile(abs(p.s.Cwd, f))
			}
		} else if signals.IsLaunchCommand(in) {
			p.s.Launches = append(p.s.Launches, ts)
		}
	}
}

func (p *parser) finishUsage() {
	if len(p.order) == 0 {
		if p.lastTotal != nil {
			u := toUsage(p.totalModel, *p.lastTotal)
			u.Calls = 0
			p.s.Usage = []model.Usage{u}
		}
		return
	}
	byModel := map[string]*model.Usage{}
	var models []string
	for _, id := range p.order {
		r := p.byResp[id]
		u := byModel[r.Model]
		if u == nil {
			u = &model.Usage{Model: r.Model, BillingProvider: r.BillingProvider}
			byModel[r.Model] = u
			models = append(models, r.Model)
		}
		u.Input += r.Input
		u.Output += r.Output
		u.CacheRead += r.CacheRead
		u.CacheWrite += r.CacheWrite
		u.Reasoning += r.Reasoning
		u.Calls += r.Calls
	}
	for _, m := range models {
		p.s.Usage = append(p.s.Usage, *byModel[m])
	}
}

func toUsage(modelName string, t tokens) model.Usage {
	if modelName == "" {
		modelName = "unknown"
	}
	fresh := t.Input - t.Cached
	if fresh < 0 {
		fresh = 0
	}
	return model.Usage{Model: modelName, BillingProvider: "openai", Input: fresh, CacheRead: t.Cached,
		CacheWrite: t.CacheWrite, Output: t.Output, Reasoning: t.Reasoning, Calls: 1}
}

func parentThread(raw json.RawMessage) string {
	var v struct {
		Subagent *struct {
			ThreadSpawn *struct {
				ParentThreadID string `json:"parent_thread_id"`
			} `json:"thread_spawn"`
		} `json:"subagent"`
	}
	if json.Unmarshal(raw, &v) != nil || v.Subagent == nil || v.Subagent.ThreadSpawn == nil {
		return ""
	}
	return v.Subagent.ThreadSpawn.ParentThreadID
}

func str(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

func commandOf(args string) string {
	var v map[string]any
	if json.Unmarshal([]byte(args), &v) != nil {
		return ""
	}
	if c, ok := v["cmd"].(string); ok {
		return c
	}
	if c, ok := v["command"].(string); ok {
		return c
	}
	if arr, ok := v["command"].([]any); ok {
		parts := make([]string, 0, len(arr))
		for _, a := range arr {
			if s, ok := a.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, " ")
	}
	return ""
}

var patchPrefixes = []string{"*** Update File: ", "*** Add File: ", "*** Delete File: "}

func patchFiles(in string) []string {
	var out []string
	for _, line := range strings.Split(in, "\n") {
		for _, pre := range patchPrefixes {
			if strings.HasPrefix(line, pre) {
				out = append(out, strings.TrimSpace(strings.TrimPrefix(line, pre)))
			}
		}
	}
	return out
}

func abs(cwd, p string) string {
	if filepath.IsAbs(p) || cwd == "" {
		return p
	}
	return filepath.Join(cwd, p)
}

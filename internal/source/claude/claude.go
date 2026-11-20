package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
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

type tokenUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	CacheRead    int64 `json:"cache_read_input_tokens"`
	CacheWrite   int64 `json:"cache_creation_input_tokens"`
}

type record struct {
	Type         string  `json:"type"`
	Cwd          string  `json:"cwd"`
	GitBranch    string  `json:"gitBranch"`
	Timestamp    string  `json:"timestamp"`
	IsMeta       bool    `json:"isMeta"`
	RequestID    string  `json:"requestId"`
	UUID         string  `json:"uuid"`
	ParentUUID   *string `json:"parentUuid"`
	LeafUUID     string  `json:"leafUuid"`
	AITitle      string  `json:"aiTitle"`
	PRNumber     int     `json:"prNumber"`
	PRRepository string  `json:"prRepository"`
	Message      *struct {
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
		Usage   *tokenUsage     `json:"usage"`
	} `json:"message"`
}

type block struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type request struct {
	model string
	usage tokenUsage
}

type parser struct {
	s        model.Session
	requests map[string]request
	order    []string
	sawUUID  bool
	skipped  int
}

func Discover(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && strings.HasSuffix(p, ".jsonl") {
			out = append(out, p)
		}
		return nil
	})
	return out, err
}

func ParseFile(path string) (model.Session, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return model.Session{}, 0, err
	}
	defer f.Close()
	p := &parser{s: model.Session{Provider: "claude", SourcePath: path}, requests: map[string]request{}}
	p.s.ID, p.s.ParentID = ids(path)
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var rec record
			if json.Unmarshal(line, &rec) != nil {
				p.skipped++
			} else {
				p.apply(&rec)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return p.s, p.skipped, err
		}
	}
	p.finishUsage()
	return p.s, p.skipped, nil
}

func ids(path string) (id, parent string) {
	base := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	dir := filepath.Dir(path)
	if filepath.Base(dir) == "subagents" {
		parent = "claude:" + filepath.Base(filepath.Dir(dir))
		return parent + "/" + base, parent
	}
	return "claude:" + base, ""
}

func (p *parser) apply(rec *record) {
	ts, _ := time.Parse(time.RFC3339Nano, rec.Timestamp)
	p.s.Touch(ts)
	if rec.Cwd != "" && p.s.Cwd == "" {
		p.s.Cwd = rec.Cwd
	}
	if rec.GitBranch != "" {
		p.s.Branch = rec.GitBranch
	}
	if rec.UUID != "" && !p.sawUUID {
		p.sawUUID = true
		if rec.ParentUUID != nil {
			p.s.HeadParentUUID = *rec.ParentUUID
		}
	}
	switch rec.Type {
	case "ai-title":
		if rec.AITitle != "" {
			p.s.Title = rec.AITitle
		}
	case "last-prompt":
		p.s.AddLeaf(rec.LeafUUID)
	case "pr-link":
		if rec.PRRepository != "" && rec.PRNumber > 0 {
			p.s.AddRefs(signals.Ref(rec.PRRepository, rec.PRNumber))
		}
	case "user":
		if !rec.IsMeta && rec.Message != nil {
			p.user(rec.Message.Content)
		}
	case "assistant":
		if rec.Message != nil {
			p.assistant(rec, ts)
		}
	}
}

func (p *parser) user(raw json.RawMessage) {
	var text string
	if len(raw) > 0 && raw[0] == '"' {
		_ = json.Unmarshal(raw, &text)
	} else {
		var blocks []block
		if json.Unmarshal(raw, &blocks) != nil {
			return
		}
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" {
				parts = append(parts, b.Text)
			}
		}
		text = strings.Join(parts, "\n")
	}
	text = signals.CleanPrompt(text)
	if signals.IsNoisePrompt(text) {
		return
	}
	p.s.AddRefs(signals.Refs(text)...)
	p.s.AddPrompt(signals.Clip(text, 400))
}

func (p *parser) assistant(rec *record, ts time.Time) {
	m := rec.Message
	key := rec.RequestID
	if key == "" {
		key = rec.UUID
	}
	if m.Usage != nil && key != "" {
		if _, seen := p.requests[key]; !seen {
			p.order = append(p.order, key)
		}
		p.requests[key] = request{model: m.Model, usage: *m.Usage}
	}
	var blocks []block
	if json.Unmarshal(m.Content, &blocks) != nil {
		return
	}
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if t := strings.TrimSpace(b.Text); t != "" {
				p.s.LastReply = signals.Clip(t, 400)
				p.s.AddRefs(signals.Refs(t)...)
			}
		case "tool_use":
			p.tool(b, ts)
		}
	}
}

func (p *parser) tool(b block, ts time.Time) {
	p.s.CountTool(b.Name)
	var in struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
		Command      string `json:"command"`
	}
	_ = json.Unmarshal(b.Input, &in)
	switch b.Name {
	case "Edit", "Write", "MultiEdit":
		p.s.AddFile(in.FilePath)
	case "NotebookEdit":
		p.s.AddFile(in.NotebookPath)
	case "Bash":
		if signals.IsLaunchCommand(in.Command) {
			p.s.Launches = append(p.s.Launches, ts)
		}
	}
}

func (p *parser) finishUsage() {
	byModel := map[string]*model.Usage{}
	var models []string
	for _, k := range p.order {
		r := p.requests[k]
		name := r.model
		if name == "" {
			name = "unknown"
		}
		u := byModel[name]
		if u == nil {
			u = &model.Usage{Model: name, BillingProvider: "anthropic"}
			byModel[name] = u
			models = append(models, name)
		}
		u.Input += r.usage.InputTokens
		u.Output += r.usage.OutputTokens
		u.CacheRead += r.usage.CacheRead
		u.CacheWrite += r.usage.CacheWrite
		u.Calls++
	}
	for _, name := range models {
		p.s.Usage = append(p.s.Usage, *byModel[name])
	}
}

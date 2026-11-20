package model

import (
	"errors"
	"time"
)

type Usage struct {
	Model           string
	BillingProvider string
	Input           int64
	Output          int64
	CacheRead       int64
	CacheWrite      int64
	Reasoning       int64
	Calls           int64
}

func (u Usage) Total() int64 { return u.Input + u.Output + u.CacheRead + u.CacheWrite }

type Session struct {
	ID             string
	Provider       string
	Profile        string
	ParentID       string
	ContinuesID    string
	SourcePath     string
	Cwd            string
	Branch         string
	Title          string
	StartedAt      time.Time
	EndedAt        time.Time
	Prompts        []string
	LastReply      string
	FilesWritten   []string
	Tools          map[string]int
	Refs           []string
	Launches       []time.Time
	HeadParentUUID string
	LeafUUIDs      []string
	Usage          []Usage `json:"-"`
}

const MaxPrompts = 12

func (s *Session) AddPrompt(p string) {
	if p == "" {
		return
	}
	if len(s.Prompts) < MaxPrompts {
		s.Prompts = append(s.Prompts, p)
		return
	}
	s.Prompts[MaxPrompts-1] = p
}

func (s *Session) AddFile(p string) { s.FilesWritten = appendUnique(s.FilesWritten, p) }
func (s *Session) AddLeaf(u string) { s.LeafUUIDs = appendUnique(s.LeafUUIDs, u) }

func (s *Session) AddRefs(refs ...string) {
	for _, r := range refs {
		s.Refs = appendUnique(s.Refs, r)
	}
}

func (s *Session) CountTool(name string) {
	if name == "" {
		return
	}
	if s.Tools == nil {
		s.Tools = map[string]int{}
	}
	s.Tools[name]++
}

func (s *Session) Touch(t time.Time) {
	if t.IsZero() {
		return
	}
	if s.StartedAt.IsZero() || t.Before(s.StartedAt) {
		s.StartedAt = t
	}
	if t.After(s.EndedAt) {
		s.EndedAt = t
	}
}

func (s Session) TotalTokens() int64 {
	var n int64
	for _, u := range s.Usage {
		n += u.Total()
	}
	return n
}

func appendUnique(list []string, v string) []string {
	if v == "" {
		return list
	}
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

type Link struct {
	A, B       string
	Source     string
	Confidence float64
	Reason     string
}

type Judgment struct {
	PairHash    string
	A, B        string
	Probability float64
	InputTokens int64
	JudgedAt    time.Time
}

type Decision struct {
	ID        int64
	Kind      string
	A, B      string
	Name      string
	CreatedAt time.Time
}

const (
	DecisionMerge  = "merge"
	DecisionUnlink = "unlink"
	DecisionDetach = "detach"
	DecisionName   = "name"
	DecisionNest   = "nest"
	DecisionUnnest = "unnest"
)

const maxNameLen = 200

func NeedsB(kind string) bool {
	return kind == DecisionMerge || kind == DecisionUnlink || kind == DecisionNest
}

func ValidDecision(d Decision) error {
	switch d.Kind {
	case DecisionMerge, DecisionUnlink, DecisionDetach, DecisionName, DecisionNest, DecisionUnnest:
	default:
		return errors.New("unknown decision kind")
	}
	if d.A == "" {
		return errors.New("decision needs A")
	}
	if NeedsB(d.Kind) && (d.B == "" || d.B == d.A) {
		return errors.New("decision needs a B different from A")
	}
	if len(d.Name) > maxNameLen {
		return errors.New("name too long")
	}
	return nil
}

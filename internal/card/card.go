package card

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"appraisal/internal/model"
	"appraisal/internal/signals"
)

const (
	timeLayout = "2006-01-02 15:04"
	maxFiles   = 10
	maxTools   = 5
	clipLen    = 200
)

func Build(s model.Session, scope string) (body, hash string) {
	var b strings.Builder
	line := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "%s: %s\n", k, v)
		}
	}
	line("Provider", s.Provider)
	line("Scope", scope)
	line("Branch", s.Branch)
	line("Title", s.Title)
	if !s.StartedAt.IsZero() {
		line("When", s.StartedAt.UTC().Format(timeLayout)+" to "+s.EndedAt.UTC().Format(timeLayout))
	}
	line("Refs", strings.Join(s.Refs, ", "))
	if prompts := pickPrompts(s.Prompts); len(prompts) > 0 {
		b.WriteString("Instructions:\n")
		for _, p := range prompts {
			fmt.Fprintf(&b, "- %s\n", signals.Clip(p, clipLen))
		}
	}
	line("Files written", strings.Join(shortFiles(s.Cwd, s.FilesWritten), ", "))
	line("Tools", topTools(s.Tools))
	line("Last reply", signals.Clip(s.LastReply, clipLen))
	body = b.String()
	sum := sha256.Sum256([]byte(body))
	return body, hex.EncodeToString(sum[:8])
}

func pickPrompts(ps []string) []string {
	if len(ps) <= 4 {
		return ps
	}
	return append(append([]string{}, ps[:3]...), ps[len(ps)-1])
}

func shortFiles(cwd string, files []string) []string {
	if len(files) > maxFiles {
		files = files[len(files)-maxFiles:]
	}
	out := make([]string, 0, len(files))
	for _, f := range files {
		if cwd != "" {
			if rel, err := filepath.Rel(cwd, f); err == nil && !strings.HasPrefix(rel, "..") {
				f = rel
			}
		}
		out = append(out, f)
	}
	return out
}

func topTools(tools map[string]int) string {
	names := make([]string, 0, len(tools))
	for n := range tools {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		if tools[names[i]] != tools[names[j]] {
			return tools[names[i]] > tools[names[j]]
		}
		return names[i] < names[j]
	})
	if len(names) > maxTools {
		names = names[:maxTools]
	}
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = fmt.Sprintf("%s×%d", n, tools[n])
	}
	return strings.Join(parts, ", ")
}

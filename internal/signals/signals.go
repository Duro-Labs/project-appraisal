package signals

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	prURL     = regexp.MustCompile(`github\.com/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)/(?:pull|issues)/(\d+)`)
	qualified = regexp.MustCompile(`(?:^|[\s(\[,])([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)#(\d+)\b`)
	pasted    = regexp.MustCompile(`</?pasted_content[^>]*>`)
	space     = regexp.MustCompile(`\s+`)
	launch    = regexp.MustCompile(`(?:^|[\s;&|(])(?:offload\s|hermes\s+chat\b)`)
)

var noisePrefixes = []string{
	"<", "Base directory for this skill:", "Caveat:", "[Request interrupted",
	"# AGENTS.md", "[ASYNC DELEGATION", "[CONTEXT COMPACTION",
}

func Ref(repo string, n int) string { return strings.ToLower(repo) + "#" + strconv.Itoa(n) }

func Refs(text string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(repo, num string) {
		r := strings.ToLower(repo) + "#" + num
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	for _, m := range prURL.FindAllStringSubmatch(text, -1) {
		add(m[1], m[2])
	}
	for _, m := range qualified.FindAllStringSubmatch(text, -1) {
		add(m[1], m[2])
	}
	return out
}

func CleanPrompt(text string) string { return strings.TrimSpace(pasted.ReplaceAllString(text, "")) }

func IsNoisePrompt(text string) bool {
	t := strings.TrimSpace(text)
	if t == "" {
		return true
	}
	for _, p := range noisePrefixes {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

func Clip(text string, n int) string {
	t := strings.TrimSpace(space.ReplaceAllString(text, " "))
	r := []rune(t)
	if len(r) <= n {
		return t
	}
	return string(r[:n]) + "…"
}

func IsLaunchCommand(cmd string) bool { return launch.MatchString(cmd) }

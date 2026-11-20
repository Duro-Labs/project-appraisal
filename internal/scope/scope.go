package scope

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"appraisal/internal/model"
)

var gittree = regexp.MustCompile(`/gittrees/([^/]+)/`)

var remotePrefixes = []string{"git@github.com:", "https://github.com/", "http://github.com/", "ssh://git@github.com/"}

type Resolver struct {
	Home      string
	VaultRoot string
	RemoteOf  func(repoRoot string) string
	cache     map[string]string
}

func NewResolver(home, vaultRoot string) *Resolver {
	return &Resolver{Home: home, VaultRoot: vaultRoot, RemoteOf: gitRemote, cache: map[string]string{}}
}

func gitRemote(root string) string {
	out, err := exec.Command("git", "-C", root, "config", "--get", "remote.origin.url").Output()
	if err != nil {
		return ""
	}
	return NormalizeRemote(string(out))
}

func NormalizeRemote(u string) string {
	u = strings.TrimSuffix(strings.TrimSpace(u), ".git")
	for _, p := range remotePrefixes {
		if strings.HasPrefix(u, p) {
			return strings.ToLower(strings.TrimPrefix(u, p))
		}
	}
	return strings.ToLower(u)
}

func (r *Resolver) Repo(cwd string) string {
	if cwd == "" {
		return ""
	}
	if v, ok := r.cache[cwd]; ok {
		return v
	}
	v := r.resolve(cwd)
	r.cache[cwd] = v
	return v
}

func (r *Resolver) resolve(cwd string) string {
	if root := findRepoRoot(cwd); root != "" && root != r.Home && root != r.VaultRoot {
		if rem := r.RemoteOf(root); rem != "" {
			return rem
		}
		return "local:" + strings.ToLower(filepath.Base(root))
	}
	if m := gittree.FindStringSubmatch(cwd + "/"); m != nil {
		return "gittree:" + strings.ToLower(m[1])
	}
	return ""
}

func findRepoRoot(dir string) string {
	d := filepath.Clean(dir)
	if _, err := os.Stat(d); err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}

func ResolveGittrees(repos map[string]string) {
	byBase := map[string]map[string]bool{}
	for _, v := range repos {
		if v == "" || strings.HasPrefix(v, "gittree:") || strings.HasPrefix(v, "local:") {
			continue
		}
		base := v[strings.LastIndex(v, "/")+1:]
		if byBase[base] == nil {
			byBase[base] = map[string]bool{}
		}
		byBase[base][v] = true
	}
	for k, v := range repos {
		name, ok := strings.CutPrefix(v, "gittree:")
		if !ok {
			continue
		}
		if cands := byBase[name]; len(cands) == 1 {
			for only := range cands {
				repos[k] = only
			}
		} else {
			repos[k] = "local:" + name
		}
	}
}

func VaultSubject(vaultRoot, cwd string, files []string) string {
	counts := map[string]int{}
	add := func(dirs []string) {
		if s := subject(dirs); s != "" {
			counts[s]++
		}
	}
	if rel, ok := under(vaultRoot, cwd); ok && rel != "." {
		add(strings.Split(rel, string(filepath.Separator)))
	}
	for _, f := range files {
		if rel, ok := under(vaultRoot, f); ok {
			parts := strings.Split(rel, string(filepath.Separator))
			add(parts[:len(parts)-1])
		}
	}
	best, bestN := "", 0
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if counts[k] > bestN {
			best, bestN = k, counts[k]
		}
	}
	return best
}

func under(root, p string) (string, bool) {
	if root == "" || p == "" {
		return "", false
	}
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

func subject(dirs []string) string {
	if len(dirs) == 0 {
		return ""
	}
	if dirs[0] == "Agents" && len(dirs) >= 3 {
		switch dirs[2] {
		case "_Inbox":
			return ""
		case "_Archive":
			if len(dirs) < 4 {
				return ""
			}
			return strings.Join([]string{dirs[0], dirs[1], dirs[3]}, "/")
		}
	}
	n := 3
	if len(dirs) < n {
		n = len(dirs)
	}
	return strings.Join(dirs[:n], "/")
}

const hoardRootKey = "HOARD_VAULT_ROOT"

func HoardRoot(getenv func(string) string, configDir string) string {
	if v := strings.TrimSpace(getenv(hoardRootKey)); v != "" {
		return expandHome(v)
	}
	data, err := os.ReadFile(filepath.Join(configDir, "hoard", "config.env"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		name, value, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(name) == hoardRootKey {
			return expandHome(strings.Trim(strings.TrimSpace(value), `'"`))
		}
	}
	return ""
}

func expandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return p
}

func Key(s model.Session, repo, vault string) string {
	switch {
	case repo != "":
		return "repo:" + repo
	case vault != "":
		return "vault:" + vault
	case s.Profile != "":
		return "hermes-profile:" + s.Profile
	case s.Cwd != "":
		return "cwd:" + s.Cwd
	}
	return ""
}

package scope

import (
	"os"
	"path/filepath"
	"testing"

	"appraisal/internal/model"
)

func TestNormalizeRemote(t *testing.T) {
	cases := map[string]string{
		"git@github.com:Duro-Labs/Demo.git":     "duro-labs/demo",
		"https://github.com/acme/x":             "acme/x",
		"ssh://git@github.com/o/r.git":          "o/r",
		"https://gitlab.example.com/team/r.git": "https://gitlab.example.com/team/r",
	}
	for in, want := range cases {
		if got := NormalizeRemote(in); got != want {
			t.Errorf("%s → %s, want %s", in, got, want)
		}
	}
}

func TestRepoResolution(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	vault := filepath.Join(home, "Vault")
	repo := filepath.Join(root, "code", "demo")
	local := filepath.Join(root, "code", "scratch")
	for _, d := range []string{filepath.Join(home, ".git"), filepath.Join(vault, ".git"), filepath.Join(repo, ".git"), filepath.Join(repo, "sub"), filepath.Join(local, ".git")} {
		os.MkdirAll(d, 0o755)
	}
	r := NewResolver(home, vault)
	r.RemoteOf = func(root string) string {
		if root == repo {
			return "duro-labs/demo"
		}
		return ""
	}
	cases := map[string]string{
		filepath.Join(repo, "sub"):     "duro-labs/demo",
		local:                          "local:scratch",
		home:                           "",
		filepath.Join(vault, "Agents"): "",
		"/nowhere/gittrees/Widget-Site/721-render/": "gittree:widget-site",
		"/nowhere/at/all": "",
		"":                "",
	}
	for cwd, want := range cases {
		if got := r.Repo(cwd); got != want {
			t.Errorf("Repo(%q) = %q, want %q", cwd, got, want)
		}
	}
}

func TestResolveGittrees(t *testing.T) {
	repos := map[string]string{"a": "acme/widget-site", "b": "gittree:widget-site", "c": "gittree:ghost", "d": "o/x", "e": "p/x", "f": "gittree:x"}
	ResolveGittrees(repos)
	if repos["b"] != "acme/widget-site" || repos["c"] != "local:ghost" || repos["f"] != "local:x" {
		t.Fatalf("got %v", repos)
	}
}

func TestVaultSubjectAndKey(t *testing.T) {
	v := "/h/Vault"
	got := VaultSubject(v, "/h", []string{"/h/Vault/Agents/personal/agent-setup/plan.md", "/h/Vault/Agents/personal/agent-setup/x/y.md", "/h/Vault/Personal/Work/career/a.md", "/elsewhere/z.go"})
	if got != "Agents/personal/agent-setup" {
		t.Fatalf("VaultSubject = %q", got)
	}
	if got := VaultSubject(v, "/h/Vault/Personal/Work/career-growth-transition/study", nil); got != "Personal/Work/career-growth-transition" {
		t.Fatalf("cwd subject = %q", got)
	}
	if Key(model.Session{Cwd: "/x"}, "o/r", "S") != "repo:o/r" ||
		Key(model.Session{Cwd: "/x"}, "", "S") != "vault:S" ||
		Key(model.Session{Cwd: "/x", Profile: "jobhunt"}, "", "") != "hermes-profile:jobhunt" ||
		Key(model.Session{Cwd: "/x"}, "", "") != "cwd:/x" ||
		Key(model.Session{}, "", "") != "" {
		t.Fatal("Key precedence wrong")
	}
}

func TestEmptyVaultRootIsOff(t *testing.T) {
	if got := VaultSubject("", "/h/Vault/Agents/personal/x", []string{"/h/Vault/Agents/personal/x/a.md"}); got != "" {
		t.Fatalf("empty vault root produced subject %q, want none", got)
	}

	root := t.TempDir()
	home := filepath.Join(root, "home")
	repo := filepath.Join(root, "code", "demo")
	for _, d := range []string{filepath.Join(home, ".git"), filepath.Join(repo, ".git"), filepath.Join(repo, "sub")} {
		os.MkdirAll(d, 0o755)
	}
	r := NewResolver(home, "")
	r.RemoteOf = func(root string) string {
		if root == repo {
			return "duro-labs/demo"
		}
		return ""
	}
	if got := r.Repo(filepath.Join(repo, "sub")); got != "duro-labs/demo" {
		t.Fatalf("Repo with empty vault root = %q, want duro-labs/demo", got)
	}
}

func TestHoardContractSubjects(t *testing.T) {
	v := "/h/Vault"
	cases := map[string]string{
		"/h/Vault/Agents/personal/agent-setup/plan.md":              "Agents/personal/agent-setup",
		"/h/Vault/Agents/personal/agent-setup/Drafts/x/post.md":     "Agents/personal/agent-setup",
		"/h/Vault/Agents/personal/_Archive/agent-setup/old.md":      "Agents/personal/agent-setup",
		"/h/Vault/Agents/personal/_Archive/agent-setup/a/b.html":    "Agents/personal/agent-setup",
		"/h/Vault/Agents/personal/_Inbox/2026-11-20-notes.md":       "",
		"/h/Vault/Agents/personal/_Inbox/scratch/2026-11-20-s/x.md": "",
		"/h/Vault/Agents/personal/_Archive/stray.md":                "",
		"/h/Vault/Personal/Work/career/a.md":                        "Personal/Work/career",
	}
	for f, want := range cases {
		if got := VaultSubject(v, "", []string{f}); got != want {
			t.Errorf("VaultSubject(%q) = %q, want %q", f, got, want)
		}
	}
	if got := VaultSubject(v, "/h/Vault/Agents/personal/_Archive/agent-setup", nil); got != "Agents/personal/agent-setup" {
		t.Errorf("archived cwd subject = %q", got)
	}
	if got := VaultSubject(v, "/h/Vault/Agents/personal/_Inbox", nil); got != "" {
		t.Errorf("inbox cwd subject = %q", got)
	}
}

func TestHoardRoot(t *testing.T) {
	cfg := t.TempDir()
	env := map[string]string{}
	getenv := func(k string) string { return env[k] }
	if got := HoardRoot(getenv, cfg); got != "" {
		t.Fatalf("no env, no file: %q", got)
	}
	os.MkdirAll(filepath.Join(cfg, "hoard"), 0o755)
	os.WriteFile(filepath.Join(cfg, "hoard", "config.env"), []byte("OTHER=1\nHOARD_VAULT_ROOT='/srv/vault'\n"), 0o600)
	if got := HoardRoot(getenv, cfg); got != "/srv/vault" {
		t.Fatalf("from file: %q", got)
	}
	home, _ := os.UserHomeDir()
	os.WriteFile(filepath.Join(cfg, "hoard", "config.env"), []byte("HOARD_VAULT_ROOT=\"~/Notes\"\n"), 0o600)
	if got := HoardRoot(getenv, cfg); got != filepath.Join(home, "Notes") {
		t.Fatalf("tilde: %q", got)
	}
	env["HOARD_VAULT_ROOT"] = "/env/vault"
	if got := HoardRoot(getenv, cfg); got != "/env/vault" {
		t.Fatalf("env wins: %q", got)
	}
}

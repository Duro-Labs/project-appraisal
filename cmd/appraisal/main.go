package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"appraisal/internal/artifact"
	"appraisal/internal/link"
	"appraisal/internal/scope"
	"appraisal/internal/store"
	"appraisal/internal/sweep"
	"appraisal/internal/web"
)

const usage = `usage: appraisal <sweep|status|serve> [flags]
  sweep   read new session logs, link and judge them
  status  what was swept, skipped and judged
  serve   review and edit groups at http://127.0.0.1:7790`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fatal(err)
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	dataHome := envOr("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	data := fs.String("data", filepath.Join(dataHome, "project-appraisal"), "data directory")
	vault := fs.String("vault", os.Getenv("APPRAISAL_VAULT"), "Obsidian Vault root (opt-in connector; default $APPRAISAL_VAULT, else hoard's HOARD_VAULT_ROOT, else off)")
	threshold := fs.Float64("threshold", 0.85, "JEV probability needed to link two sessions")
	noJudge := fs.Bool("no-judge", false, "sweep: skip JEV calls")
	maxJudgments := fs.Int("max-judgments", 500, "sweep: JEV call budget for this run")
	addr := fs.String("addr", "127.0.0.1:7790", "serve: loopback address")
	fs.Parse(os.Args[2:])
	if *vault == "" {
		*vault = scope.HoardRoot(os.Getenv, envOr("XDG_CONFIG_HOME", filepath.Join(home, ".config")))
	}
	if *vault != "" {
		*vault = filepath.Clean(*vault)
	}

	st, err := store.Open(filepath.Join(*data, "appraisal.db"))
	if err != nil {
		fatal(err)
	}
	defer st.Close()

	switch cmd {
	case "sweep":
		hermesHome := envOr("HERMES_HOME", defaultHermesHome(home, dataHome))
		cfg := sweep.Config{
			ClaudeProjects: filepath.Join(envOr("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude")), "projects"),
			ClaudeArchives: claudeArchives(*data),
			CodexHome:      envOr("CODEX_HOME", filepath.Join(home, ".codex")),
			HermesHome:     hermesHome,
			VaultRoot:      *vault,
			Home:           home,
			Exclude:        []string{filepath.Join(home, ".claude-sessions-board"), *data},
			MaxJudgments:   *maxJudgments,
			Threshold:      *threshold,
			Window:         14 * 24 * time.Hour,
			Neighbours:     8,
			Log:            os.Stderr,
		}
		if !*noJudge {
			if key := link.LoadKey(hermesHome); key != "" {
				cfg.Judge = link.NewJEV(key)
			} else {
				fmt.Fprintln(os.Stderr, "no TYPESAFE_API_KEY found; sweeping without JEV")
			}
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		rep, err := sweep.Run(ctx, st, cfg)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("files read %d, unchanged %d, errors %d\nrule links %d, candidate pairs %d, judged %d, judge errors %d, pending %d\n",
			rep.FilesRead, rep.FilesUnchanged, rep.FileErrors, rep.RuleLinks, rep.Candidates, rep.Judged, rep.JudgeErrors, rep.Pending)
	case "status":
		if err := sweep.Status(st, *threshold, os.Stdout); err != nil {
			fatal(err)
		}
	case "serve":
		if !loopback(*addr) {
			fatal(fmt.Errorf("refusing non-loopback address %q", *addr))
		}
		srv := &web.Server{Store: st, Threshold: *threshold, Artifacts: artifact.Config{
			ClaudeHome: envOr("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude")),
			CodexHome:  envOr("CODEX_HOME", filepath.Join(home, ".codex")),
			HermesHome: envOr("HERMES_HOME", defaultHermesHome(home, dataHome)),
			SharedHome: filepath.Join(home, ".agents"),
		}}
		fmt.Printf("http://%s\n", *addr)
		if err := http.ListenAndServe(*addr, srv.Handler()); err != nil {
			fatal(err)
		}
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
}

func claudeArchives(data string) []string {
	var roots []string
	for _, dir := range glob(filepath.Join(data, "archives", "claude", "*")) {
		if dirExists(filepath.Join(dir, "projects")) {
			roots = append(roots, filepath.Join(dir, "projects"))
		}
	}
	return roots
}

func glob(pattern string) []string { paths, _ := filepath.Glob(pattern); return paths }

func loopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func defaultHermesHome(home, dataHome string) string {
	if p := filepath.Join(dataHome, "hermes"); dirExists(p) {
		return p
	}
	return filepath.Join(home, ".hermes")
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "appraisal:", err)
	os.Exit(1)
}

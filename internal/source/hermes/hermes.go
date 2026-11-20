package hermes

import (
	"database/sql"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"appraisal/internal/model"
	"appraisal/internal/signals"
)

const ParserVersion = 1

type DB struct{ Path, Profile string }

func Discover(home string) []DB {
	var out []DB
	if _, err := os.Stat(filepath.Join(home, "state.db")); err == nil {
		out = append(out, DB{Path: filepath.Join(home, "state.db"), Profile: "default"})
	}
	matches, _ := filepath.Glob(filepath.Join(home, "profiles", "*", "state.db"))
	sort.Strings(matches)
	for _, m := range matches {
		out = append(out, DB{Path: m, Profile: filepath.Base(filepath.Dir(m))})
	}
	return out
}

func Stamp(path string) (size, mtime int64) {
	for _, p := range []string{path, path + "-wal"} {
		if fi, err := os.Stat(p); err == nil {
			size += fi.Size()
			if m := fi.ModTime().UnixNano(); m > mtime {
				mtime = m
			}
		}
	}
	return
}

func Read(d DB) ([]model.Session, error) {
	db, err := sql.Open("sqlite", "file:"+d.Path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	cols, err := columns(db, "sessions")
	if err != nil {
		return nil, err
	}
	sessions, order, err := readSessions(db, d, cols)
	if err != nil {
		return nil, err
	}
	if has, _ := tableExists(db, "session_model_usage"); has {
		err = readModelUsage(db, d, sessions)
	} else {
		err = readSessionCounters(db, d, cols, sessions)
	}
	if err != nil {
		return nil, err
	}
	if err := readMessages(db, d, sessions); err != nil {
		return nil, err
	}
	out := make([]model.Session, 0, len(order))
	for _, id := range order {
		out = append(out, *sessions[id])
	}
	return out, nil
}

func sid(d DB, id string) string { return "hermes:" + d.Profile + ":" + id }

func col(cols map[string]bool, name, fallback string) string {
	if cols[name] {
		return "COALESCE(" + name + "," + fallback + ")"
	}
	return fallback
}

func readSessions(db *sql.DB, d DB, cols map[string]bool) (map[string]*model.Session, []string, error) {
	q := fmt.Sprintf(`SELECT id, %s, %s, %s, %s, started_at, %s, %s FROM sessions`,
		col(cols, "parent_session_id", "''"), col(cols, "cwd", "''"), col(cols, "git_branch", "''"),
		col(cols, "title", "''"), col(cols, "ended_at", "0"), col(cols, "last_activity_at", "0"))
	rows, err := db.Query(q)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := map[string]*model.Session{}
	var order []string
	for rows.Next() {
		var id, parent, cwd, branch, title string
		var started, ended, last float64
		if err := rows.Scan(&id, &parent, &cwd, &branch, &title, &started, &ended, &last); err != nil {
			return nil, nil, err
		}
		s := &model.Session{ID: sid(d, id), Provider: "hermes", Profile: d.Profile, SourcePath: d.Path,
			Cwd: cwd, Branch: branch, Title: title}
		if parent != "" {
			s.ParentID = sid(d, parent)
		}
		for _, f := range []float64{started, ended, last} {
			if f > 0 {
				s.Touch(unix(f))
			}
		}
		out[s.ID] = s
		order = append(order, s.ID)
	}
	return out, order, rows.Err()
}

func readModelUsage(db *sql.DB, d DB, sessions map[string]*model.Session) error {
	rows, err := db.Query(`SELECT session_id, model, billing_provider, SUM(api_call_count), SUM(input_tokens), SUM(output_tokens),
		SUM(cache_read_tokens), SUM(cache_write_tokens), SUM(reasoning_tokens)
		FROM session_model_usage GROUP BY session_id, model, billing_provider`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var u model.Usage
		if err := rows.Scan(&id, &u.Model, &u.BillingProvider, &u.Calls, &u.Input, &u.Output, &u.CacheRead, &u.CacheWrite, &u.Reasoning); err != nil {
			return err
		}
		if s := sessions[sid(d, id)]; s != nil {
			s.Usage = append(s.Usage, u)
		}
	}
	return rows.Err()
}

func readSessionCounters(db *sql.DB, d DB, cols map[string]bool, sessions map[string]*model.Session) error {
	q := fmt.Sprintf(`SELECT id, %s, %s, %s, %s, %s, %s, %s, %s FROM sessions`,
		col(cols, "model", "''"), col(cols, "billing_provider", "''"), col(cols, "api_call_count", "0"),
		col(cols, "input_tokens", "0"), col(cols, "output_tokens", "0"), col(cols, "cache_read_tokens", "0"),
		col(cols, "cache_write_tokens", "0"), col(cols, "reasoning_tokens", "0"))
	rows, err := db.Query(q)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var u model.Usage
		if err := rows.Scan(&id, &u.Model, &u.BillingProvider, &u.Calls, &u.Input, &u.Output, &u.CacheRead, &u.CacheWrite, &u.Reasoning); err != nil {
			return err
		}
		if s := sessions[sid(d, id)]; s != nil && u.Total() > 0 {
			s.Usage = append(s.Usage, u)
		}
	}
	return rows.Err()
}

func readMessages(db *sql.DB, d DB, sessions map[string]*model.Session) error {
	rows, err := db.Query(`SELECT session_id, role, COALESCE(content,''), COALESCE(tool_name,'') FROM messages
		WHERE role IN ('user','assistant','tool') ORDER BY session_id, id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, role, content, tool string
		if err := rows.Scan(&id, &role, &content, &tool); err != nil {
			return err
		}
		s := sessions[sid(d, id)]
		if s == nil {
			continue
		}
		switch role {
		case "user":
			text := signals.CleanPrompt(content)
			if !signals.IsNoisePrompt(text) {
				s.AddRefs(signals.Refs(text)...)
				s.AddPrompt(signals.Clip(text, 400))
			}
		case "assistant":
			if t := strings.TrimSpace(content); t != "" {
				s.LastReply = signals.Clip(t, 400)
				s.AddRefs(signals.Refs(t)...)
			}
		case "tool":
			s.CountTool(tool)
		}
	}
	return rows.Err()
}

func columns(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out[n] = true
	}
	return out, rows.Err()
}

func tableExists(db *sql.DB, name string) (bool, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n)
	return n > 0, err
}

func unix(f float64) time.Time {
	sec, frac := math.Modf(f)
	return time.Unix(int64(sec), int64(frac*1e9)).UTC()
}

package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"appraisal/internal/model"
)

const schema = `
CREATE TABLE IF NOT EXISTS files (
  path TEXT PRIMARY KEY, provider TEXT NOT NULL, size INTEGER NOT NULL, mtime INTEGER NOT NULL,
  parser_version INTEGER NOT NULL, skipped INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS sessions (
  id TEXT PRIMARY KEY, provider TEXT NOT NULL, source_path TEXT NOT NULL, started_at INTEGER,
  data TEXT NOT NULL, repo TEXT NOT NULL DEFAULT '', scope TEXT NOT NULL DEFAULT '',
  card TEXT NOT NULL DEFAULT '', card_hash TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS sessions_source ON sessions(source_path);
CREATE TABLE IF NOT EXISTS usage (
  session_id TEXT NOT NULL, model TEXT NOT NULL, billing_provider TEXT NOT NULL,
  input INTEGER, output INTEGER, cache_read INTEGER, cache_write INTEGER, reasoning INTEGER, calls INTEGER,
  PRIMARY KEY (session_id, model, billing_provider));
CREATE TABLE IF NOT EXISTS links (
  a TEXT NOT NULL, b TEXT NOT NULL, source TEXT NOT NULL, confidence REAL NOT NULL, reason TEXT NOT NULL,
  PRIMARY KEY (a, b, source));
CREATE TABLE IF NOT EXISTS judgments (
  pair_hash TEXT PRIMARY KEY, a TEXT NOT NULL, b TEXT NOT NULL, probability REAL NOT NULL,
  input_tokens INTEGER NOT NULL, judged_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS decisions (
  id INTEGER PRIMARY KEY AUTOINCREMENT, kind TEXT NOT NULL, a TEXT NOT NULL, b TEXT NOT NULL,
  name TEXT NOT NULL, created_at INTEGER NOT NULL);
`

type Store struct{ db *sql.DB }

type FileRow struct {
	Path, Provider string
	Size, Mtime    int64
	ParserVersion  int
	Skipped        int
}

type Derived struct{ SessionID, Repo, Scope, Card, CardHash string }

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) FileUnchanged(path string, size, mtime int64, pv int) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM files WHERE path=? AND size=? AND mtime=? AND parser_version=?`,
		path, size, mtime, pv).Scan(&n)
	return n == 1, err
}

func (s *Store) ReplaceFile(f FileRow, sessions []model.Session) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM usage WHERE session_id IN (SELECT id FROM sessions WHERE source_path=?)`, f.Path); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE source_path=?`, f.Path); err != nil {
		return err
	}
	for _, ss := range sessions {
		data, err := json.Marshal(ss)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO sessions(id, provider, source_path, started_at, data) VALUES (?,?,?,?,?)`,
			ss.ID, ss.Provider, f.Path, ss.StartedAt.Unix(), string(data)); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM usage WHERE session_id=?`, ss.ID); err != nil {
			return err
		}
		for _, u := range ss.Usage {
			if _, err := tx.Exec(`INSERT OR REPLACE INTO usage VALUES (?,?,?,?,?,?,?,?,?)`,
				ss.ID, u.Model, u.BillingProvider, u.Input, u.Output, u.CacheRead, u.CacheWrite, u.Reasoning, u.Calls); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(`INSERT OR REPLACE INTO files VALUES (?,?,?,?,?,?)`,
		f.Path, f.Provider, f.Size, f.Mtime, f.ParserVersion, f.Skipped); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Sessions() ([]model.Session, error) {
	usage := map[string][]model.Usage{}
	rows, err := s.db.Query(`SELECT session_id, model, billing_provider, input, output, cache_read, cache_write, reasoning, calls FROM usage`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var u model.Usage
		if err := rows.Scan(&id, &u.Model, &u.BillingProvider, &u.Input, &u.Output, &u.CacheRead, &u.CacheWrite, &u.Reasoning, &u.Calls); err != nil {
			rows.Close()
			return nil, err
		}
		usage[id] = append(usage[id], u)
	}
	rows.Close()
	rows, err = s.db.Query(`SELECT data FROM sessions ORDER BY started_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Session
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var ss model.Session
		if err := json.Unmarshal([]byte(data), &ss); err != nil {
			return nil, err
		}
		ss.Usage = usage[ss.ID]
		out = append(out, ss)
	}
	return out, rows.Err()
}

func (s *Store) SaveDerived(rows []Derived) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, d := range rows {
		res, err := tx.Exec(`UPDATE sessions SET repo=?, scope=?, card=?, card_hash=? WHERE id=?`,
			d.Repo, d.Scope, d.Card, d.CardHash, d.SessionID)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return fmt.Errorf("store: unknown session %q", d.SessionID)
		}
	}
	return tx.Commit()
}

func (s *Store) Derived() (map[string]Derived, error) {
	rows, err := s.db.Query(`SELECT id, repo, scope, card, card_hash FROM sessions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Derived{}
	for rows.Next() {
		var d Derived
		if err := rows.Scan(&d.SessionID, &d.Repo, &d.Scope, &d.Card, &d.CardHash); err != nil {
			return nil, err
		}
		out[d.SessionID] = d
	}
	return out, rows.Err()
}

func (s *Store) ReplaceRuleLinks(links []model.Link) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM links WHERE source LIKE 'rule:%'`); err != nil {
		return err
	}
	for _, l := range links {
		if _, err := tx.Exec(`INSERT OR REPLACE INTO links VALUES (?,?,?,?,?)`, l.A, l.B, l.Source, l.Confidence, l.Reason); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Links() ([]model.Link, error) {
	rows, err := s.db.Query(`SELECT a, b, source, confidence, reason FROM links`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Link
	for rows.Next() {
		var l model.Link
		if err := rows.Scan(&l.A, &l.B, &l.Source, &l.Confidence, &l.Reason); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) HasJudgment(pairHash string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM judgments WHERE pair_hash=?`, pairHash).Scan(&n)
	return n > 0, err
}

func (s *Store) SaveJudgment(j model.Judgment) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO judgments VALUES (?,?,?,?,?,?)`,
		j.PairHash, j.A, j.B, j.Probability, j.InputTokens, j.JudgedAt.UnixNano())
	return err
}

func (s *Store) Judgments() ([]model.Judgment, error) {
	rows, err := s.db.Query(`SELECT pair_hash, a, b, probability, input_tokens, judged_at FROM judgments j
		WHERE judged_at = (SELECT MAX(judged_at) FROM judgments k WHERE k.a = j.a AND k.b = j.b)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Judgment
	for rows.Next() {
		var j model.Judgment
		var at int64
		if err := rows.Scan(&j.PairHash, &j.A, &j.B, &j.Probability, &j.InputTokens, &at); err != nil {
			return nil, err
		}
		j.JudgedAt = time.Unix(0, at)
		out = append(out, j)
	}
	return out, rows.Err()
}

func (s *Store) JudgmentStats() (calls, inputTokens int64, err error) {
	err = s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(input_tokens),0) FROM judgments`).Scan(&calls, &inputTokens)
	return
}

func (s *Store) AddDecision(d model.Decision) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO decisions(kind, a, b, name, created_at) VALUES (?,?,?,?,?)`,
		d.Kind, d.A, d.B, d.Name, d.CreatedAt.UnixNano())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) Decisions() ([]model.Decision, error) {
	rows, err := s.db.Query(`SELECT id, kind, a, b, name, created_at FROM decisions ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Decision
	for rows.Next() {
		var d model.Decision
		var at int64
		if err := rows.Scan(&d.ID, &d.Kind, &d.A, &d.B, &d.Name, &at); err != nil {
			return nil, err
		}
		d.CreatedAt = time.Unix(0, at)
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) Skips() (map[string]int64, error) {
	rows, err := s.db.Query(`SELECT provider, SUM(skipped) FROM files GROUP BY provider`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var p string
		var n int64
		if err := rows.Scan(&p, &n); err != nil {
			return nil, err
		}
		out[p] = n
	}
	return out, rows.Err()
}

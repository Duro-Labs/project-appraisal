package hermes

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func makeDB(t *testing.T, path string, withUsage bool) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stmts := []string{
		`CREATE TABLE sessions (id TEXT PRIMARY KEY, source TEXT, parent_session_id TEXT, cwd TEXT, git_branch TEXT, title TEXT,
		  started_at REAL NOT NULL, ended_at REAL, last_activity_at REAL, model TEXT, billing_provider TEXT,
		  input_tokens INTEGER, output_tokens INTEGER, cache_read_tokens INTEGER, cache_write_tokens INTEGER, reasoning_tokens INTEGER, api_call_count INTEGER)`,
		`CREATE TABLE messages (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT, role TEXT, content TEXT, tool_name TEXT, timestamp REAL)`,
		`INSERT INTO sessions VALUES ('p1','tui',NULL,'/data/Code/x','main','Plan the week',1000.5,2000,2100,'m1','openrouter',10,2,100,0,0,3)`,
		`INSERT INTO sessions VALUES ('c1','subagent','p1','/data/Code/x',NULL,NULL,1500,1600,NULL,'m2','openrouter',5,1,0,0,0,1)`,
		`INSERT INTO messages(session_id, role, content) VALUES ('p1','user','Plan the week for Duro-Labs/demo#9')`,
		`INSERT INTO messages(session_id, role, content) VALUES ('p1','assistant','First answer')`,
		`INSERT INTO messages(session_id, role, content, tool_name) VALUES ('p1','tool','ok','terminal')`,
		`INSERT INTO messages(session_id, role, content) VALUES ('p1','assistant','')`,
		`INSERT INTO messages(session_id, role, content) VALUES ('p1','assistant','Final answer')`,
	}
	if withUsage {
		stmts = append(stmts,
			`CREATE TABLE session_model_usage (session_id TEXT, model TEXT, billing_provider TEXT, task TEXT, api_call_count INTEGER,
			  input_tokens INTEGER, output_tokens INTEGER, cache_read_tokens INTEGER, cache_write_tokens INTEGER, reasoning_tokens INTEGER)`,
			`INSERT INTO session_model_usage VALUES ('p1','m1','openrouter','',2,8,2,90,0,0)`,
			`INSERT INTO session_model_usage VALUES ('p1','m1','openrouter','title_generation',1,2,0,10,0,0)`,
			`INSERT INTO session_model_usage VALUES ('c1','m2','openrouter','',1,5,1,0,0,0)`)
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

func TestReadWithModelUsage(t *testing.T) {
	home := t.TempDir()
	makeDB(t, filepath.Join(home, "state.db"), true)
	makeDB(t, filepath.Join(home, "profiles", "jobhunt", "state.db"), false)
	dbs := Discover(home)
	if len(dbs) != 2 || dbs[0].Profile != "default" || dbs[1].Profile != "jobhunt" {
		t.Fatalf("Discover = %+v", dbs)
	}
	ss, err := Read(dbs[0])
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]int{}
	for i, s := range ss {
		by[s.ID] = i
	}
	p, c := ss[by["hermes:default:p1"]], ss[by["hermes:default:c1"]]
	if c.ParentID != "hermes:default:p1" || p.ParentID != "" || p.Profile != "default" || p.Provider != "hermes" {
		t.Fatalf("ids: p=%+v c=%+v", p, c)
	}
	if p.Cwd != "/data/Code/x" || p.Branch != "main" || p.Title != "Plan the week" {
		t.Errorf("meta: %+v", p)
	}
	if len(p.Usage) != 1 || p.Usage[0].Input != 10 || p.Usage[0].CacheRead != 100 || p.Usage[0].Calls != 3 || p.Usage[0].BillingProvider != "openrouter" {
		t.Errorf("usage summed across tasks = %+v", p.Usage)
	}
	if len(p.Prompts) != 1 || p.LastReply != "Final answer" || p.Tools["terminal"] != 1 || len(p.Refs) != 1 {
		t.Errorf("messages: prompts=%v last=%q tools=%v refs=%v", p.Prompts, p.LastReply, p.Tools, p.Refs)
	}
	if p.StartedAt.Unix() != 1000 || p.EndedAt.Unix() != 2100 {
		t.Errorf("times %v %v", p.StartedAt, p.EndedAt)
	}
}

func TestReadFallsBackToSessionCounters(t *testing.T) {
	home := t.TempDir()
	makeDB(t, filepath.Join(home, "state.db"), false)
	ss, err := Read(DB{Path: filepath.Join(home, "state.db"), Profile: "default"})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range ss {
		if s.ID == "hermes:default:p1" && (len(s.Usage) != 1 || s.Usage[0].Model != "m1" || s.Usage[0].Input != 10 || s.Usage[0].Calls != 3) {
			t.Fatalf("fallback usage = %+v", s.Usage)
		}
	}
}

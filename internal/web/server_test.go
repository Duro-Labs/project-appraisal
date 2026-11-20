package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"appraisal/internal/model"
	"appraisal/internal/store"
)

func server(t *testing.T) http.Handler {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ss := []model.Session{
		{ID: "claude:a", Provider: "claude", SourcePath: "/f", Title: "A", StartedAt: time.Unix(10, 0), Usage: []model.Usage{{Model: "m", Input: 5}}},
		{ID: "claude:b", Provider: "claude", SourcePath: "/f", Title: "B", StartedAt: time.Unix(20, 0), Usage: []model.Usage{{Model: "m", Input: 7}}},
	}
	st.ReplaceFile(store.FileRow{Path: "/f", Provider: "claude"}, ss)
	st.SaveDerived([]store.Derived{{SessionID: "claude:a", Scope: "repo:o/r", Card: "card A"}})
	return (&Server{Store: st, Threshold: 0.85}).Handler()
}

type groupsResp struct {
	Groups []struct {
		ID, Name  string
		Roots     []string
		TreeTotal int64
	}
	Stale int
}

func do(t *testing.T, h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	if req.Host == "example.com" {
		req.Host = "127.0.0.1:7790"
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestPageAndGroups(t *testing.T) {
	h := server(t)
	if w := do(t, h, httptest.NewRequest("GET", "/", nil)); w.Code != 200 || !strings.Contains(w.Body.String(), "<title>Project appraisal</title>") {
		t.Fatalf("page: %d", w.Code)
	}
	w := do(t, h, httptest.NewRequest("GET", "/api/groups", nil))
	var g groupsResp
	json.Unmarshal(w.Body.Bytes(), &g)
	if len(g.Groups) != 2 || g.Groups[0].TreeTotal != 7 {
		t.Fatalf("groups = %+v", g)
	}
	w = do(t, h, httptest.NewRequest("GET", "/api/session?id=claude:a", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "card A") {
		t.Fatalf("session: %d %s", w.Code, w.Body.String())
	}
	if w := do(t, h, httptest.NewRequest("GET", "/api/session?id=nope", nil)); w.Code != 404 {
		t.Fatalf("missing session: %d", w.Code)
	}
}

func post(body, origin, ctype string) *http.Request {
	r := httptest.NewRequest("POST", "/api/decisions", strings.NewReader(body))
	r.Host = "127.0.0.1:7790"
	if ctype != "" {
		r.Header.Set("Content-Type", ctype)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	return r
}

func TestDecisions(t *testing.T) {
	h := server(t)
	w := do(t, h, post(`{"Kind":"merge","A":"claude:a","B":"claude:b"}`, "http://127.0.0.1:7790", "application/json"))
	var g groupsResp
	json.Unmarshal(w.Body.Bytes(), &g)
	if w.Code != 200 || len(g.Groups) != 1 || g.Groups[0].TreeTotal != 12 {
		t.Fatalf("merge: %d %+v", w.Code, g)
	}
	w = do(t, h, post(`{"Kind":"name","A":"claude:b","Name":"Feature X"}`, "", "application/json"))
	json.Unmarshal(w.Body.Bytes(), &g)
	if g.Groups[0].Name != "Feature X" {
		t.Fatalf("name: %+v", g)
	}
	if w := do(t, h, post(`{"Kind":"merge","A":"claude:a","B":"claude:b"}`, "https://evil.example", "application/json")); w.Code != 403 {
		t.Fatalf("foreign origin: %d", w.Code)
	}
	if w := do(t, h, post(`{"Kind":"merge","A":"claude:a","B":"claude:b"}`, "", "text/plain")); w.Code != 415 {
		t.Fatalf("content type: %d", w.Code)
	}
	if w := do(t, h, post(`{"Kind":"explode","A":"claude:a"}`, "", "application/json")); w.Code != 400 {
		t.Fatalf("invalid decision: %d", w.Code)
	}
}

func TestHostAllowlist(t *testing.T) {
	h := server(t)
	get := func(host string) int {
		r := httptest.NewRequest("GET", "/api/groups", nil)
		r.Host = host
		return do(t, h, r).Code
	}
	if c := get("evil.example:7790"); c != 403 {
		t.Fatalf("foreign host: %d", c)
	}
	for _, host := range []string{"localhost:7790", "127.0.0.1:7790", "[::1]:7790", "localhost", "127.0.0.2:1"} {
		if c := get(host); c != 200 {
			t.Fatalf("%s: %d", host, c)
		}
	}
	r := post(`{"Kind":"merge","A":"claude:a","B":"claude:b"}`, "http://evil.example:7790", "application/json")
	r.Host = "evil.example:7790"
	if w := do(t, h, r); w.Code != 403 {
		t.Fatalf("rebinding POST: %d", w.Code)
	}
}

func TestSplitRecipeKeepsRemainderTogether(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	var ss []model.Session
	for i, id := range []string{"claude:c", "claude:x", "claude:y", "claude:d"} {
		ss = append(ss, model.Session{ID: id, Provider: "claude", SourcePath: "/f", StartedAt: time.Unix(int64(10+i), 0), Usage: []model.Usage{{Model: "m", Input: 1}}})
	}
	if err := st.ReplaceFile(store.FileRow{Path: "/f", Provider: "claude"}, ss); err != nil {
		t.Fatal(err)
	}
	link := func(a, b string) model.Link {
		return model.Link{A: a, B: b, Source: "rule:test", Confidence: 1}
	}
	if err := st.ReplaceRuleLinks([]model.Link{link("claude:c", "claude:x"), link("claude:x", "claude:y"), link("claude:y", "claude:d")}); err != nil {
		t.Fatal(err)
	}
	h := (&Server{Store: st, Threshold: 0.85}).Handler()

	var g groupsResp
	json.Unmarshal(do(t, h, httptest.NewRequest("GET", "/api/groups", nil)).Body.Bytes(), &g)
	if len(g.Groups) != 1 {
		t.Fatalf("setup: want 1 group, got %+v", g)
	}

	for _, body := range []string{
		`{"Kind":"merge","A":"claude:c","B":"claude:d"}`,
		`{"Kind":"detach","A":"claude:x"}`,
		`{"Kind":"detach","A":"claude:y"}`,
		`{"Kind":"merge","A":"claude:x","B":"claude:y"}`,
	} {
		w := do(t, h, post(body, "", "application/json"))
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body.String())
		}
		g = groupsResp{}
		json.Unmarshal(w.Body.Bytes(), &g)
	}
	if len(g.Groups) != 2 {
		t.Fatalf("want 2 groups, got %+v", g)
	}
	sets := map[string]bool{}
	for _, gr := range g.Groups {
		r := append([]string(nil), gr.Roots...)
		sort.Strings(r)
		sets[strings.Join(r, ",")] = true
	}
	if !sets["claude:c,claude:d"] || !sets["claude:x,claude:y"] {
		t.Fatalf("groups = %+v", g.Groups)
	}
}

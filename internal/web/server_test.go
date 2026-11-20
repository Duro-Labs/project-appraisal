package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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

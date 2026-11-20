package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"appraisal/internal/artifact"
	"appraisal/internal/store"
)

func TestArtifactReadEditConflictAndDelete(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "agents")
	if err := os.MkdirAll(shared, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(shared, "RULES.md")
	if err := os.WriteFile(path, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(root, "appraisal.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	h := (&Server{Store: st, Artifacts: artifact.Config{SharedHome: shared}}).Handler()
	w := do(t, h, httptest.NewRequest("GET", "/api/artifacts", nil))
	var listed struct{ Items []artifact.Item }
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil || w.Code != 200 || len(listed.Items) != 1 {
		t.Fatalf("list: %d %+v, %v", w.Code, listed, err)
	}
	item := listed.Items[0]
	w = do(t, h, httptest.NewRequest("GET", "/api/artifact?id="+item.ID, nil))
	var opened struct {
		Item    artifact.Item
		Content string
	}
	if err := json.Unmarshal(w.Body.Bytes(), &opened); err != nil || opened.Content != "before" {
		t.Fatalf("read: %d %+v, %v", w.Code, opened, err)
	}
	w = do(t, h, artifactRequest("PUT", map[string]string{"ID": item.ID, "Hash": item.Hash, "Content": "after"}))
	if err := json.Unmarshal(w.Body.Bytes(), &opened); err != nil || w.Code != 200 || opened.Content != "after" {
		t.Fatalf("save: %d %+v, %v", w.Code, opened, err)
	}
	w = do(t, h, artifactRequest("DELETE", map[string]string{"ID": item.ID, "Hash": item.Hash}))
	if w.Code != 409 {
		t.Fatalf("stale delete: %d", w.Code)
	}
	w = do(t, h, artifactRequest("DELETE", map[string]string{"ID": item.ID, "Hash": opened.Item.Hash}))
	if w.Code != 200 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file still exists: %v", err)
	}
	if w := do(t, h, httptest.NewRequest("GET", "/api/artifact?id=../../etc/passwd", nil)); w.Code != 404 {
		t.Fatalf("arbitrary path: %d", w.Code)
	}
}

func TestArtifactMutationsRejectInvalidConfigAndForeignOrigin(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "hooks.json")
	if err := os.WriteFile(path, []byte(`{"hooks":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(root, "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	h := (&Server{Store: st, Artifacts: artifact.Config{CodexHome: root}}).Handler()
	var listed struct{ Items []artifact.Item }
	w := do(t, h, httptest.NewRequest("GET", "/api/artifacts", nil))
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil || len(listed.Items) != 1 {
		t.Fatalf("list = %+v, %v", listed, err)
	}
	item := listed.Items[0]
	request := artifactRequest("PUT", map[string]string{"ID": item.ID, "Hash": item.Hash, "Content": "{"})
	if w := do(t, h, request); w.Code != 400 {
		t.Fatalf("invalid JSON: %d", w.Code)
	}
	request = artifactRequest("PUT", map[string]string{"ID": item.ID, "Hash": item.Hash, "Content": `{"hooks":[]}`})
	request.Header.Set("Origin", "https://evil.example")
	if w := do(t, h, request); w.Code != 403 {
		t.Fatalf("foreign origin: %d", w.Code)
	}
	if w := do(t, h, artifactRequest("DELETE", map[string]string{"ID": item.ID, "Hash": item.Hash})); w.Code != 403 {
		t.Fatalf("hook config deletion: %d", w.Code)
	}
}

func artifactRequest(method string, body any) *http.Request {
	data, _ := json.Marshal(body)
	r := httptest.NewRequest(method, "/api/artifact", bytes.NewReader(data))
	r.Host = "127.0.0.1:7790"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://127.0.0.1:7790")
	return r
}

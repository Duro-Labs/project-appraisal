package link

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJEVSame(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k1" {
			t.Errorf("auth header = %q", r.Header.Get("Authorization"))
		}
		var body struct {
			Model     string `json:"model"`
			State     string `json:"state"`
			Questions map[string]struct {
				Type string `json:"type"`
			} `json:"questions"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Model != "jev-latest" || body.Questions["same"].Type != "noul" || !strings.Contains(body.State, "Session A:\ncard-a") {
			t.Errorf("payload = %+v", body)
		}
		w.Write([]byte(`{"answers":{"same":{"noul":0.91}},"usage":{"input_tokens":321}}`))
	}))
	defer srv.Close()
	j := NewJEV("k1")
	j.Endpoint = srv.URL
	v, err := j.Same(context.Background(), "card-a", "card-b")
	if err != nil || v.Probability != 0.91 || v.InputTokens != 321 {
		t.Fatalf("verdict = %+v, %v", v, err)
	}
}

func TestJEVErrorsHideKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusPaymentRequired)
	}))
	defer srv.Close()
	j := NewJEV("secret-key")
	j.Endpoint = srv.URL
	_, err := j.Same(context.Background(), "a", "b")
	if err == nil || strings.Contains(err.Error(), "secret-key") || !strings.Contains(err.Error(), "402") {
		t.Fatalf("err = %v", err)
	}
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"answers":{}}`)) }))
	defer empty.Close()
	j.Endpoint = empty.URL
	if _, err := j.Same(context.Background(), "a", "b"); err == nil {
		t.Fatal("missing answer must be an error")
	}
}

func TestLoadKeyAndPairHash(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, ".env"), []byte("OTHER=1\nTYPESAFE_API_KEY='from-env-file'\n"), 0o600)
	t.Setenv("TYPESAFE_API_KEY", "")
	if got := LoadKey(home); got != "from-env-file" {
		t.Fatalf("LoadKey = %q", got)
	}
	t.Setenv("TYPESAFE_API_KEY", "from-environment")
	if got := LoadKey(home); got != "from-environment" {
		t.Fatalf("LoadKey env = %q", got)
	}
	if PairHash("aa", "bb") != PairHash("bb", "aa") || len(PairHash("aa", "bb")) != 32 {
		t.Fatal("PairHash must be symmetric, 32 hex chars")
	}
}

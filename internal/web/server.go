package web

import (
	_ "embed"
	"encoding/json"
	"mime"
	"net/http"
	"net/url"
	"time"

	"appraisal/internal/group"
	"appraisal/internal/model"
	"appraisal/internal/store"
	"appraisal/internal/sweep"
)

//go:embed index.html
var page []byte

const maxBody = 64 << 10

type Server struct {
	Store     *store.Store
	Threshold float64
}

type sessionView struct {
	ID, Provider, Title, Scope string
	Start                      time.Time
	Tokens                     int64
	Child                      bool
}

type groupView struct {
	ID, Name, Suggested, ParentID, Scope string
	Roots, Children                      []string
	Totals                               group.Totals
	TreeTotal                            int64
	Start, End                           time.Time
	Sessions                             []sessionView
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(page)
	})
	mux.HandleFunc("GET /api/groups", s.groups)
	mux.HandleFunc("GET /api/session", s.session)
	mux.HandleFunc("POST /api/decisions", s.decide)
	return mux
}

func (s *Server) groups(w http.ResponseWriter, r *http.Request) {
	in, derived, err := sweep.Load(s.Store, s.Threshold)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	gs, stale := group.Build(in)
	byID := map[string]model.Session{}
	for _, ss := range in.Sessions {
		byID[ss.ID] = ss
	}
	views := make([]groupView, 0, len(gs))
	for _, g := range gs {
		v := groupView{ID: g.ID, Name: g.Name, Suggested: g.Suggested, ParentID: g.ParentID, Scope: g.Scope,
			Roots: g.Roots, Children: g.Children, Totals: g.Totals, TreeTotal: g.TreeTotal, Start: g.Start, End: g.End}
		for _, id := range g.Sessions {
			ss := byID[id]
			v.Sessions = append(v.Sessions, sessionView{ID: id, Provider: ss.Provider, Title: ss.Title, Scope: derived[id].Scope,
				Start: ss.StartedAt, Tokens: ss.TotalTokens(), Child: ss.ParentID != ""})
		}
		views = append(views, v)
	}
	writeJSON(w, map[string]any{"groups": views, "stale": len(stale)})
}

func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	in, derived, err := sweep.Load(s.Store, s.Threshold)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, ss := range in.Sessions {
		if ss.ID == id {
			writeJSON(w, map[string]any{"ID": ss.ID, "Provider": ss.Provider, "Title": ss.Title, "Cwd": ss.Cwd,
				"Branch": ss.Branch, "Start": ss.StartedAt, "End": ss.EndedAt, "Usage": ss.Usage, "Card": derived[id].Card})
			return
		}
	}
	http.NotFound(w, r)
}

func (s *Server) decide(w http.ResponseWriter, r *http.Request) {
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Host != r.Host {
			http.Error(w, "cross-origin request refused", http.StatusForbidden)
			return
		}
	}
	if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct != "application/json" {
		http.Error(w, "send application/json", http.StatusUnsupportedMediaType)
		return
	}
	var d model.Decision
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&d); err != nil {
		http.Error(w, "bad JSON", http.StatusBadRequest)
		return
	}
	d.ID = 0
	if err := model.ValidDecision(d); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	d.CreatedAt = time.Now()
	if _, err := s.Store.AddDecision(d); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.groups(w, r)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

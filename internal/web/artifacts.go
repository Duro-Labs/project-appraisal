package web

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"net/url"

	"appraisal/internal/artifact"
)

const artifactBodyLimit = 512 << 10

func (s *Server) artifactConfig() (artifact.Config, error) {
	cfg := s.Artifacts
	sessions, err := s.Store.Sessions()
	if err != nil {
		return cfg, err
	}
	for _, session := range sessions {
		if session.Cwd != "" {
			cfg.Workspaces = append(cfg.Workspaces, session.Cwd)
		}
	}
	return cfg, nil
}

func (s *Server) artifacts(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.artifactConfig()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	items, err := artifact.Search(cfg, r.URL.Query().Get("q"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"items": items})
}

func (s *Server) artifact(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.artifactConfig()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	item, body, err := artifact.Read(cfg, r.URL.Query().Get("id"))
	if err != nil {
		artifactError(w, err)
		return
	}
	writeJSON(w, map[string]any{"item": item, "content": body})
}

func (s *Server) saveArtifact(w http.ResponseWriter, r *http.Request) {
	if !artifactMutationAllowed(w, r) {
		return
	}
	var req struct{ ID, Hash, Content string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, artifactBodyLimit)).Decode(&req); err != nil || req.ID == "" || req.Hash == "" {
		http.Error(w, "bad JSON", http.StatusBadRequest)
		return
	}
	cfg, err := s.artifactConfig()
	if err == nil {
		err = artifact.Save(cfg, req.ID, req.Hash, req.Content)
	}
	if err != nil {
		artifactError(w, err)
		return
	}
	item, body, err := artifact.Read(cfg, req.ID)
	if err != nil {
		artifactError(w, err)
		return
	}
	writeJSON(w, map[string]any{"item": item, "content": body})
}

func (s *Server) deleteArtifact(w http.ResponseWriter, r *http.Request) {
	if !artifactMutationAllowed(w, r) {
		return
	}
	var req struct{ ID, Hash string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&req); err != nil || req.ID == "" || req.Hash == "" {
		http.Error(w, "bad JSON", http.StatusBadRequest)
		return
	}
	cfg, err := s.artifactConfig()
	if err == nil {
		err = artifact.Delete(cfg, req.ID, req.Hash)
	}
	if err != nil {
		artifactError(w, err)
		return
	}
	s.artifacts(w, r)
}

func artifactMutationAllowed(w http.ResponseWriter, r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Host != r.Host {
			http.Error(w, "cross-origin request refused", http.StatusForbidden)
			return false
		}
	}
	if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct != "application/json" {
		http.Error(w, "send application/json", http.StatusUnsupportedMediaType)
		return false
	}
	return true
}

func artifactError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, artifact.ErrNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, artifact.ErrConflict):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, artifact.ErrReadOnly), errors.Is(err, artifact.ErrTooLarge):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, artifact.ErrInvalidContent):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

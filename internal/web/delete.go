package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"appraisal/internal/model"
	"appraisal/internal/source/hermes"
)

func (s *Server) deleteSession(ctx context.Context, id string, transcript bool) error {
	ss, err := s.Store.Session(id)
	if err != nil {
		return err
	}
	if !transcript {
		return s.Store.RemoveSessions([]string{id}, "")
	}
	if !s.canDeleteTranscript(ss) {
		return errTranscriptUnavailable
	}
	if ss.Provider == "hermes" {
		return s.deleteHermes(ctx, ss)
	}
	rows, err := s.Store.Sessions()
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.ID != id && row.SourcePath == ss.SourcePath {
			return errSharedSource
		}
	}
	if err := checkTranscriptPath(ss.SourcePath, s.sourceRoot(ss.Provider)); err != nil {
		return err
	}
	if err := removeTranscriptFile(ss.SourcePath); err != nil {
		return err
	}
	return s.Store.RemoveSessions([]string{id}, ss.SourcePath)
}

var errTranscriptUnavailable = errors.New("transcript deletion is unavailable for this session")
var errSharedSource = errors.New("source file contains multiple sessions")

func (s *Server) canDeleteTranscript(ss model.Session) bool {
	switch ss.Provider {
	case "claude", "codex":
		if filepath.Ext(ss.SourcePath) != ".jsonl" || !within(s.sourceRoot(ss.Provider), ss.SourcePath) {
			return false
		}
		return true
	case "hermes":
		if ss.Profile == "" || ss.Profile == "." || ss.Profile == ".." || ss.Profile != filepath.Base(ss.Profile) {
			return false
		}
		prefix := "hermes:" + ss.Profile + ":"
		if !strings.HasPrefix(ss.ID, prefix) || len(ss.ID) == len(prefix) {
			return false
		}
		path := filepath.Join(s.HermesHome, "profiles", ss.Profile, "state.db")
		if ss.Profile == "default" {
			path = filepath.Join(s.HermesHome, "state.db")
		}
		return s.HermesHome != "" && filepath.Clean(ss.SourcePath) == filepath.Clean(path)
	}
	return false
}

func (s *Server) sourceRoot(provider string) string {
	if provider == "claude" {
		return s.ClaudeRoot
	}
	return s.CodexRoot
}

func within(root, path string) bool {
	if !filepath.IsAbs(root) || !filepath.IsAbs(path) {
		return false
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "."
}

func checkTranscriptPath(path, root string) error {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	resolvedParent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return err
	}
	if !within(resolvedRoot, filepath.Join(resolvedParent, filepath.Base(path))) {
		return errTranscriptUnavailable
	}
	return nil
}

func removeTranscriptFile(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("transcript is not a regular file")
	}
	return os.Remove(path)
}

func (s *Server) deleteHermes(ctx context.Context, ss model.Session) error {
	before, err := s.Store.Sessions()
	if err != nil {
		return err
	}
	deleteFn := s.DeleteHermes
	if deleteFn == nil {
		deleteFn = deleteHermesCLI
	}
	rawID := strings.TrimPrefix(ss.ID, "hermes:"+ss.Profile+":")
	deleteErr := deleteFn(ctx, ss.Profile, rawID)
	after, err := hermes.Read(hermes.DB{Path: ss.SourcePath, Profile: ss.Profile})
	if err != nil {
		if deleteErr != nil {
			return deleteErr
		}
		return fmt.Errorf("Hermes deleted the source session, but Appraisal could not refresh it: %w", err)
	}
	present := make(map[string]bool, len(after))
	for _, row := range after {
		present[row.ID] = true
	}
	if present[ss.ID] {
		if deleteErr != nil {
			return deleteErr
		}
		return fmt.Errorf("Hermes reported success, but the session is still in its database")
	}
	var removed []string
	for _, row := range before {
		if row.SourcePath == ss.SourcePath && !present[row.ID] {
			removed = append(removed, row.ID)
		}
	}
	return s.Store.RemoveSessions(removed, "")
}

func deleteHermesCLI(ctx context.Context, profile, id string) error {
	cmd := exec.CommandContext(ctx, "hermes", "-p", profile, "sessions", "delete", id, "--yes")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("Hermes refused session deletion: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func isMissingSession(err error) bool { return errors.Is(err, sql.ErrNoRows) }

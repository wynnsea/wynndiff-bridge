package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"wynndiff-bridge/internal/agent"
	"wynndiff-bridge/internal/config"
	"wynndiff-bridge/internal/git"
)

type Server struct {
	cfg    config.Config
	gitSvc *git.Service
	runner *agent.Runner
}

func New(cfg config.Config) http.Handler {
	s := &Server{
		cfg:    cfg,
		gitSvc: git.NewService(cfg.RepoDir),
		runner: agent.NewRunner(cfg.RepoDir, cfg.OpenRouterAPIKey, cfg.AIGatewayURL),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /git/status", s.handleGitDiff)
	mux.HandleFunc("GET /git/diff", s.handleGitDiff)
	mux.HandleFunc("POST /git/commit", s.handleGitCommit)
	mux.HandleFunc("POST /git/reset", s.handleGitReset)
	mux.HandleFunc("POST /prompt", s.handlePrompt)

	return s.withCORSAndAuth(mux)
}

func (s *Server) withCORSAndAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if s.cfg.BridgeToken != "" {
			auth := strings.TrimSpace(r.Header.Get("Authorization"))
			expected := "Bearer " + s.cfg.BridgeToken
			if auth != expected {
				writeJSON(w, http.StatusUnauthorized, map[string]any{
					"ok":    false,
					"error": "Unauthorized: BRIDGE_TOKEN tidak cocok",
				})
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	info := s.gitSvc.Info()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                true,
		"hostName":          s.cfg.HostName,
		"repoFullName":      info.RepoFullName,
		"branch":            info.Branch,
		"repoDir":           s.cfg.RepoDir,
		"changedFilesCount": info.ChangedCount,
	})
}

func (s *Server) handleGitDiff(w http.ResponseWriter, r *http.Request) {
	snap, err := s.gitSvc.Diff()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *Server) handleGitCommit(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Message string `json:"message"`
		Push    *bool  `json:"push"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "JSON tidak valid"})
		return
	}
	shouldPush := true
	if body.Push != nil {
		shouldPush = *body.Push
	}
	out, err := s.gitSvc.CommitAndPush(body.Message, shouldPush)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "output": out})
}

func (s *Server) handleGitReset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := s.gitSvc.Reset(body.Path); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handlePrompt(w http.ResponseWriter, r *http.Request) {
	var req agent.PromptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Payload prompt tidak valid"})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)

	emit := func(ev agent.StreamEvent) {
		b, err := json.Marshal(ev)
		if err != nil {
			return
		}
		_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
		if flusher != nil {
			flusher.Flush()
		}
	}

	if err := s.runner.Execute(r.Context(), req, emit); err != nil {
		emit(agent.StreamEvent{
			Type:    "error",
			Message: err.Error(),
		})
	}
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

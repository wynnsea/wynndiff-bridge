package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"wynndiff-bridge/internal/agent"
	"wynndiff-bridge/internal/config"
)

func TestBearerAuthAndHealth(t *testing.T) {
	h := New(config.Config{
		RepoDir:     ".",
		BridgeToken: "secret-123",
		HostName:    "Test-PC",
	})

	// Missing token -> 401
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", rec.Code)
	}

	// Valid token -> 200
	req2 := httptest.NewRequest(http.MethodGet, "/health", nil)
	req2.Header.Set("Authorization", "Bearer secret-123")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 with valid token, got %d", rec2.Code)
	}
}

func TestResolveSafePathBlocksTraversal(t *testing.T) {
	if _, err := agent.ResolveSafePath("/tmp/demo-repo", "../etc/passwd"); err == nil {
		t.Fatal("expected traversal path to be rejected")
	}
	if _, err := agent.ResolveSafePath("/tmp/demo-repo", "lib/main.dart"); err != nil {
		t.Fatalf("expected valid relative path to pass, got %v", err)
	}
}

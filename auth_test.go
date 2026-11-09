package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/server"
	"github.com/starefossen/home/libs/mcpauth"
)

func TestHandlerAuth(t *testing.T) {
	dir := t.TempDir()
	tok := "0123456789abcdef0123456789abcdef"
	if err := os.MkdirAll(filepath.Join(dir, "read"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "read", "varden"), []byte(tok+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	auth, err := mcpauth.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	sse := server.NewSSEServer(server.NewMCPServer("t", "1"), server.WithBaseURL("http://localhost"))
	srv := httptest.NewServer(newHandler(auth, sse))
	defer srv.Close()

	get := func(path, bearer string) int {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	if c := get("/health", ""); c != http.StatusOK {
		t.Errorf("/health = %d", c)
	}
	if c := get("/sse", ""); c != http.StatusUnauthorized {
		t.Errorf("/sse no header = %d", c)
	}
	if c := get("/sse?token="+tok, ""); c != http.StatusUnauthorized {
		t.Errorf("/sse query token = %d", c)
	}
	if c := get("/message", ""); c != http.StatusUnauthorized {
		t.Errorf("/message no header = %d", c)
	}
	if c := get("/message", "wrong-token-wrong-token-wrong"); c != http.StatusUnauthorized {
		t.Errorf("/message bad token = %d", c)
	}
	if c := get("/sse", tok); c != http.StatusOK {
		t.Errorf("/sse valid token = %d", c)
	}
}

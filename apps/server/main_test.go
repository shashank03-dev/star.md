package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDenyDotfiles(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("index.html", "ok")
	write("file.txt", "ok")
	write("release.v2.txt", "ok") // a dot inside a segment is fine
	write(".env", "SECRET=1")
	write(".git/config", "[core]")

	h := denyDotfiles(http.FileServer(http.Dir(dir)))
	cases := []struct {
		path string
		want int
	}{
		{"/", http.StatusOK},
		{"/index.html", http.StatusMovedPermanently}, // FileServer canonicalizes to "/"
		{"/file.txt", http.StatusOK},
		{"/release.v2.txt", http.StatusOK},
		{"/.env", http.StatusNotFound},
		{"/.git", http.StatusNotFound},
		{"/.git/config", http.StatusNotFound},
		{"/foo/.hidden", http.StatusNotFound},
		{"/..env", http.StatusNotFound},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("GET %s = %d, want %d", tc.path, rec.Code, tc.want)
		}
	}
}

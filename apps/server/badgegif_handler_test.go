package main

import (
	"bytes"
	"context"
	"encoding/json"
	"image/gif"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGalaxyGIFHTTPContract(t *testing.T) {
	contract := galaxyGIFFixture()
	data, err := json.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	handler := NewGalaxyGIFHandler(func(ctx context.Context, repo string) ([]byte, error) {
		calls++
		if repo != contract.Repo {
			t.Fatalf("unexpected repo %q", repo)
		}
		return data, nil
	})
	path := "/api/badge.gif?repo=acme/stellar"
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, path, nil))
	if first.Code != http.StatusOK || first.Header().Get("Content-Type") != "image/gif" || first.Header().Get("ETag") == "" {
		t.Fatalf("invalid GIF response: status=%d headers=%v", first.Code, first.Header())
	}
	if !bytes.HasPrefix(first.Body.Bytes(), []byte("GIF89a")) {
		t.Fatal("response does not contain a GIF")
	}
	if _, err := gif.DecodeAll(bytes.NewReader(first.Body.Bytes())); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method string
		etag   string
		status int
	}{
		{http.MethodGet, first.Header().Get("ETag"), http.StatusNotModified},
		{http.MethodHead, "", http.StatusOK},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(tc.method, path, nil)
		r.Header.Set("If-None-Match", tc.etag)
		handler.ServeHTTP(w, r)
		if w.Code != tc.status || w.Body.Len() != 0 {
			t.Fatalf("conditional %s: got status=%d body=%d", tc.method, w.Code, w.Body.Len())
		}
	}
	if calls != 3 {
		t.Fatalf("expected 3 contract reads, got %d", calls)
	}
}

func TestGalaxyGIFHTTPInputAndInternalFailures(t *testing.T) {
	handler := NewGalaxyGIFHandler(func(context.Context, string) ([]byte, error) {
		t.Fatal("should not attempt build")
		return nil, nil
	})
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{http.MethodGet, "/api/badge.gif", 400},
		{http.MethodGet, "/api/badge.gif?repo=a/..", 400},
		{http.MethodGet, "/api/badge.gif?repo=a/b&repo=c/d", 400},
		{http.MethodPost, "/api/badge.gif?repo=a/b", 405},
	} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s %s: status=%d", tc.method, tc.path, w.Code)
		}
	}
	handler = NewGalaxyGIFHandler(func(context.Context, string) ([]byte, error) {
		return nil, &apiError{status: 503, code: "busy", msg: "busy"}
	})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/badge.gif?repo=a/b", nil))
	if w.Code != 503 || !strings.Contains(w.Body.String(), "busy") {
		t.Fatalf("incorrect busy response: %d %s", w.Code, w.Body.String())
	}
}

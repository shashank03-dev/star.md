package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func badgeFixture() Contract {
	return Contract{
		Repo: "acme/stellar", HeadSHA: "aabbccd", CommitTotal: 158,
		Scale: Scale{Stars: 5, Span: 26, Turns: 2.15, Duration: 14},
		Main: []MainNode{
			{Type: "genesis", Tier: 4, SHA: "genesis", BirthT: 0},
			{Type: "commit", Tier: 1, SHA: "0000001", BirthT: 0.17},
			{Type: "commit", Tier: 2, SHA: "0000002", BirthT: 0.38},
			{Type: "release", Tier: 3, SHA: "0000003", Label: "v1.0", BirthT: 0.70},
			{Type: "commit", Tier: 1, SHA: "0000004", BirthT: 1},
		},
		Branches: []BranchNode{
			{N: "feature/nebula", Sub: "branch · 4 commits", At: 0.4, K: 4, Kind: "ref"},
			{N: "fix/glow", Sub: "PR #12 · 3 commits", At: 0.7, K: 3, Kind: "pr"},
		},
	}
}

func TestRenderBadgeSVGDeterministicAndRepoDerived(t *testing.T) {
	c := badgeFixture()
	a, err := RenderBadgeSVG("acme/stellar", c, "medium")
	if err != nil {
		t.Fatal(err)
	}
	b, err := RenderBadgeSVG("acme/stellar", c, "medium")
	if err != nil || !bytes.Equal(a, b) {
		t.Fatalf("same contract produced different SVGs: %v", err)
	}
	for _, want := range []string{"<svg", "viewBox=", "<path", "<circle", "acme/stellar", "158 commits", "2 constellations", "genesis"} {
		if !bytes.Contains(a, []byte(want)) {
			t.Errorf("SVG missing %q", want)
		}
	}
	dec := xml.NewDecoder(bytes.NewReader(a))
	for {
		_, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("invalid SVG XML: %v", err)
		}
	}
	c.Main[2].SHA = "different"
	changed, err := RenderBadgeSVG("acme/stellar", c, "medium")
	if err != nil || bytes.Equal(a, changed) {
		t.Fatalf("changing contract history did not change badge: %v", err)
	}
	c = badgeFixture()
	c.Branches[0].N = "different-branch"
	changed, err = RenderBadgeSVG("acme/stellar", c, "medium")
	if err != nil || bytes.Equal(a, changed) {
		t.Fatalf("changing branch did not change badge: %v", err)
	}
}

func TestRenderBadgeSVGEscapesUntrustedText(t *testing.T) {
	c := badgeFixture()
	c.Branches[0].N = `</title><script>alert("x")</script>&`
	c.Branches[0].Sub = `bad & <markup>`
	c.Main[3].Label = `v<evil>&`
	svg, err := RenderBadgeSVG("acme/stellar", c, "medium")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(svg, []byte("<script")) || bytes.Contains(svg, []byte("</title><script")) {
		t.Fatal("unescaped branch name injected markup")
	}
	if !bytes.Contains(svg, []byte("&lt;script&gt;")) || !bytes.Contains(svg, []byte("&amp;")) {
		t.Fatal("untrusted branch name was not XML escaped")
	}
}

func TestRenderBadgeSVGSingleCommitHasOnlyGenesis(t *testing.T) {
	c := badgeFixture()
	c.Main = c.Main[:1]
	c.CommitTotal = 1
	c.Scale = computeScale(1)
	c.Branches = nil
	svg, err := RenderBadgeSVG(c.Repo, c, "medium")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(svg, []byte(`stroke="url(#filament)"`)) {
		t.Fatal("one-commit repo already displays mature spiral arms")
	}
	if !bytes.Contains(svg, []byte("GENESIS")) {
		t.Fatal("one-commit repo has no genesis marker")
	}
}

func TestRenderBadgeSVGBoundsAndSizes(t *testing.T) {
	c := badgeFixture()
	for size, width := range map[string]string{"compact": `width="600"`, "medium": `width="840"`, "large": `width="1080"`} {
		out, err := RenderBadgeSVG(c.Repo, c, size)
		if err != nil || !bytes.Contains(out, []byte(width)) {
			t.Errorf("size=%q: expected %s: %v", size, width, err)
		}
	}
	for _, name := range []string{"", "owner/../repo", "../repo", "a/."} {
		if _, err := RenderBadgeSVG(name, c, "medium"); err == nil {
			t.Errorf("accepted invalid repo %q", name)
		}
	}
	if _, err := RenderBadgeSVG(c.Repo, c, "huge"); err == nil {
		t.Error("accepted unsupported size")
	}
	c.Main = nil
	if _, err := RenderBadgeSVG(c.Repo, c, "medium"); err == nil {
		t.Error("accepted empty history")
	}
	c = badgeFixture()
	c.Main = make([]MainNode, maxStars+1)
	if _, err := RenderBadgeSVG(c.Repo, c, "medium"); err == nil {
		t.Error("accepted overlong history")
	}
	c = badgeFixture()
	c.Branches = make([]BranchNode, branchCap+1)
	if _, err := RenderBadgeSVG(c.Repo, c, "medium"); err == nil {
		t.Error("accepted overlong branches")
	}
}

func TestNewBadgeHandlerResponseCacheAndConditional(t *testing.T) {
	c := badgeFixture()
	blob, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	hits := 0
	h := NewBadgeHandler(func(_ context.Context, name string) ([]byte, error) {
		hits++
		if name != c.Repo {
			t.Fatalf("loader got %q", name)
		}
		return blob, nil
	})
	req := httptest.NewRequest(http.MethodGet, "/api/badge?repo=acme/stellar&size=medium", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "image/svg+xml") {
		t.Fatalf("got status %d, type %q: %s", w.Code, w.Header().Get("Content-Type"), w.Body.String())
	}
	if w.Header().Get("Cache-Control") == "" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing cache or content sniffing headers")
	}
	etag := w.Header().Get("ETag")
	if etag == "" {
		t.Fatal("missing etag")
	}
	second := httptest.NewRequest(http.MethodGet, req.URL.String(), nil)
	second.Header.Set("If-None-Match", etag)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, second)
	if w2.Code != http.StatusNotModified || w2.Body.Len() != 0 {
		t.Fatalf("conditional request returned %d, %d bytes", w2.Code, w2.Body.Len())
	}
	w3 := httptest.NewRecorder()
	h.ServeHTTP(w3, httptest.NewRequest(http.MethodHead, req.URL.String(), nil))
	if w3.Code != http.StatusOK || w3.Body.Len() != 0 || w3.Header().Get("ETag") != etag {
		t.Fatalf("HEAD gave code=%d body=%d etag=%q", w3.Code, w3.Body.Len(), w3.Header().Get("ETag"))
	}
	if hits != 3 {
		t.Errorf("loader called %d times, expected 3", hits)
	}
}

func TestNewBadgeHandlerRejectsBadRequestBeforeBuild(t *testing.T) {
	h := NewBadgeHandler(func(context.Context, string) ([]byte, error) {
		t.Error("loader called with invalid request")
		return nil, nil
	})
	for _, tc := range []struct {
		method, target string
		status         int
	}{
		{http.MethodGet, "/api/badge", 400},
		{http.MethodGet, "/api/badge?repo=../danger", 400},
		{http.MethodGet, "/api/badge?repo=a/..", 400},
		{http.MethodGet, "/api/badge?repo=a/b&repo=c/d", 400},
		{http.MethodGet, "/api/badge?repo=a/b&size=giant", 400},
		{http.MethodPost, "/api/badge?repo=a/b", http.StatusMethodNotAllowed},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(tc.method, tc.target, nil))
		if w.Code != tc.status {
			t.Errorf("%s %s: got %d, want %d", tc.method, tc.target, w.Code, tc.status)
		}
	}
}

func TestNewBadgeHandlerErrorStatuses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		data   []byte
		err    error
		status int
	}{
		{"missing", nil, errNotFound, http.StatusNotFound},
		{"rate limited", nil, errRateLimit, http.StatusServiceUnavailable},
		{"failed", nil, errors.New("db secret"), http.StatusInternalServerError},
		{"malformed contract", []byte("{"), nil, http.StatusBadGateway},
		{"empty contract", []byte(`{"main":[]}`), nil, http.StatusUnprocessableEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewBadgeHandler(func(context.Context, string) ([]byte, error) { return tc.data, tc.err })
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/badge?repo=acme/stellar", nil))
			if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("got %d, headers %v, body %q", w.Code, w.Header(), w.Body.String())
			}
			if strings.Contains(w.Body.String(), "db secret") {
				t.Fatal("leaked internal error")
			}
		})
	}
}

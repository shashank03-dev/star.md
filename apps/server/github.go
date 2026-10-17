package main

// What is left of the GitHub REST API in this service.
//
// The commit graph, branches and tags all come from git now (gitsource.go), so
// the REST budget - 5,000 req/hr per token, the ceiling this rearchitecture
// exists to escape - is spent on exactly two things:
//
//   - merged pull requests, when a repo's own history carries no PR references
//     (a rebase-merge workflow leaves nothing to parse). At most 9 calls, only
//     on the repos that need it.
//   - authenticating a private repo's clone.
//
// A public repo now costs zero REST calls, no matter how large it is.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// ---- typed errors so the HTTP layer can map to status codes ----

type apiError struct {
	status     int    // HTTP status to return to our client
	code       string // machine code in the JSON body
	msg        string
	retryAfter time.Duration // Retry-After hint for 429s; 0 means none
}

func (e *apiError) Error() string { return e.msg }

var (
	errNotFound  = &apiError{status: 404, code: "not_found", msg: "repository not found"}
	errRateLimit = &apiError{status: 503, code: "rate_limited", msg: "github rate limit reached, retry later"}
	errEmptyRepo = &apiError{status: 422, code: "empty_repo", msg: "repository has no commits"}
)

// ---- token pool ----

// tokenPool spreads the residual REST calls (and private-repo clones) over as
// many credentials as the operator configured. GITHUB_TOKENS takes a
// comma-separated list; GITHUB_TOKEN remains the single-token form. A GitHub
// App installation token works here too - mint it outside and pass it in.
//
// The pool is optional by design: with no token at all every public repo still
// charts, because the history comes from anonymous git clones.
type tokenPool struct {
	tokens []string
	n      atomic.Uint32
}

func newTokenPool() *tokenPool {
	var out []string
	raw := getenv("GITHUB_TOKENS")
	if strings.TrimSpace(raw) == "" {
		raw = getenv("GITHUB_TOKEN")
	}
	for _, t := range strings.Split(raw, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return &tokenPool{tokens: out}
}

// next returns the next token round-robin, or "" when none is configured.
func (p *tokenPool) next() string {
	if len(p.tokens) == 0 {
		return ""
	}
	i := p.n.Add(1) - 1
	return p.tokens[int(i)%len(p.tokens)]
}

func (p *tokenPool) size() int { return len(p.tokens) }

// ---- GitHub REST client ----

type ghClient struct {
	tokens *tokenPool
	http   *http.Client
}

func newGHClient(tokens *tokenPool) *ghClient {
	return &ghClient{tokens: tokens, http: &http.Client{Timeout: 45 * time.Second}}
}

// get performs a single GET against the REST API and returns the body.
func (c *ghClient) get(ctx context.Context, path string) ([]byte, error) {
	url := "https://api.github.com/" + strings.TrimPrefix(path, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "stellar-starmap")
	if t := c.tokens.next(); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	// Every REST call spends the quota this service exists to conserve, so each
	// one is logged: "no rest: lines" is the operable proof that a build stayed
	// inside the git transport.
	log.Printf("rest: GET %s", path)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// Bounded: a 100-PR list page is well under this, and a misbehaving proxy
	// must not be able to make one build allocate without limit.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}

	switch {
	case resp.StatusCode == 404:
		return nil, errNotFound
	case resp.StatusCode == 409: // "Git Repository is empty"
		return nil, errEmptyRepo
	case resp.StatusCode == 403 || resp.StatusCode == 429:
		if resp.Header.Get("X-RateLimit-Remaining") == "0" ||
			strings.Contains(strings.ToLower(string(body)), "rate limit") {
			return nil, errRateLimit
		}
		return nil, &apiError{status: 502, code: "github_error", msg: "github 403: " + string(body)}
	case resp.StatusCode >= 300:
		return nil, &apiError{status: 502, code: "github_error",
			msg: fmt.Sprintf("github %d: %s", resp.StatusCode, string(body))}
	}
	return body, nil
}

// ---- merged pull requests (the only resource still fetched over REST) ----

type ghPull struct {
	Number   int        `json:"number"`
	MergedAt *time.Time `json:"merged_at"`
	Head     struct {
		Ref string `json:"ref"`
	} `json:"head"`
}

// mergedPulls returns merged PRs for repos whose git history carries no PR
// references. One paginated list call, plus one detail call per PR actually
// rendered (at most branchCap), so the worst case is 9 requests.
func (c *ghClient) mergedPulls(ctx context.Context, repo string) ([]prMerge, error) {
	body, err := c.get(ctx, "repos/"+repo+"/pulls?state=closed&per_page=100&sort=updated&direction=desc")
	if err != nil {
		return nil, err
	}
	var ps []ghPull
	if err := json.Unmarshal(body, &ps); err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	out := make([]prMerge, 0, branchCap)
	for _, p := range ps {
		if len(out) >= branchCap {
			break
		}
		if p.MergedAt == nil || p.Head.Ref == "" || seen[p.Head.Ref] {
			continue
		}
		seen[p.Head.Ref] = true
		out = append(out, prMerge{Number: p.Number, Ref: p.Head.Ref, MergedAt: *p.MergedAt})
	}

	// Commit counts, one at a time: GitHub's best-practices guide is explicit
	// that secondary rate limits are avoided by serial, not concurrent,
	// requests. A failure just leaves Commits at 0, which renders as an
	// unknown-size PR rather than an error.
	for i := range out {
		body, err := c.get(ctx, fmt.Sprintf("repos/%s/pulls/%d", repo, out[i].Number))
		if err != nil {
			continue
		}
		var d struct {
			Commits int `json:"commits"`
		}
		if json.Unmarshal(body, &d) == nil && d.Commits > 0 {
			out[i].Commits = d.Commits
		}
	}
	return out, nil
}

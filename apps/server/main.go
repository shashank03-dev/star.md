package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

func getenv(k string) string { return os.Getenv(k) }

type server struct {
	gh     *ghClient  // residual REST: merged PRs only
	git    *gitSource // the history itself
	cache  Cache
	lease  *redisCache // cross-instance build coordination on Vercel
	limits limits      // per-visitor request and cold-build budgets
}

var repoRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func configuredCache(dbPath string) Cache {
	var cache Cache
	url, token := getenv("UPSTASH_REDIS_REST_URL"), getenv("UPSTASH_REDIS_REST_TOKEN")
	if url == "" && token == "" {
		// Vercel's Upstash Marketplace integration injects these KV-prefixed
		// credentials automatically. Preserve the explicit Upstash override for
		// deployments that configure it themselves.
		url, token = getenv("KV_REST_API_URL"), getenv("KV_REST_API_TOKEN")
	}
	if getenv("VERCEL") != "" && (url == "" || token == "") {
		// Vercel has ephemeral storage. A local SQLite cache here would make
		// cold requests build independently on every Function instance.
		log.Print("cache: Vercel requires both Upstash Redis REST variables")
		return nil
	}
	if url != "" || token != "" {
		if c, err := newRedisCache(url, token); err != nil {
			log.Printf("cache: redis configuration invalid: %v", err)
		} else {
			cache = c
		}
	} else {
		if c, err := newSQLiteCache(dbPath); err != nil {
			log.Printf("cache: init failed (%v) - running without cache", err)
		} else {
			cache = c
		}
	}
	return cache
}

func main() {
	staticDir := getenv("STATIC_DIR")
	if staticDir == "" {
		staticDir = "."
	}
	port := getenv("PORT")
	if port == "" {
		port = "8770"
	}
	dbPath := getenv("CACHE_DB")
	if dbPath == "" {
		dbPath = "stellar-cache.db"
	}

	cache := configuredCache(dbPath)
	tokens := newTokenPool()
	git := newGitSource(tokens)
	s := &server{gh: newGHClient(tokens), git: git, cache: bestEffort{inner: cache}, limits: configuredLimits(cache)}
	if shared, ok := cache.(*redisCache); ok {
		s.lease = shared
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if _, err := exec.LookPath("git"); err != nil || cache == nil && getenv("VERCEL") != "" {
			http.Error(w, "backend unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"activeAnalyses":  len(git.sem),
			"waitingAnalyses": git.waiting.Load(),
			"maxAnalyses":     cap(git.sem),
			"maxWaiting":      git.queueMax,
		})
	})
	if getenv("VERCEL") != "" && cache == nil {
		// A production Function without a shared cache would stampede GitHub
		// on every visit. Fail closed until Redis is configured correctly.
		unavailable := func(w http.ResponseWriter, _ *http.Request) {
			writeErr(w, &apiError{status: 503, code: "cache_unavailable", msg: "shared cache is not configured"})
		}
		mux.HandleFunc("/api/starmap", unavailable)
		mux.HandleFunc("/api/starmap/stream", unavailable)
		mux.HandleFunc("/api/badge", unavailable)
		mux.HandleFunc("/api/badge.gif", unavailable)
	} else {
		mux.HandleFunc("/api/starmap", s.rateLimited(s.handleStarmap, false))
		mux.HandleFunc("/api/starmap/stream", s.rateLimited(s.handleStarmapStream, true))
		mux.HandleFunc("/api/badge", s.handleBadge)
		mux.HandleFunc("/api/badge.gif", s.handleBadgeGIF)
	}
	if getenv("VERCEL") == "" && staticDir != "-" {
		mux.Handle("/", denyDotfiles(http.FileServer(http.Dir(staticDir))))
	}

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: mux,
		// No WriteTimeout: cold builds legitimately run for minutes and the
		// stream endpoint writes for as long as they do. Reads are different:
		// every endpoint is a GET whose body is ignored, so a tight read
		// budget costs nothing and closes the socket-hold a trickled request
		// would otherwise get.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	log.Printf("stellar-server listening on :%s (static=%s, cache=%s, mirrors=%s, tokens=%d)",
		port, staticDir, dbPath, git.dir, tokens.size())
	stopCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	failed := make(chan error, 1)
	go func() { failed <- srv.ListenAndServe() }()
	select {
	case err := <-failed:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http: %v", err)
		}
	case <-stopCtx.Done():
		log.Print("http: draining connections")
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("http: graceful shutdown incomplete: %v", err)
			_ = srv.Close()
		}
	}
}

// denyDotfiles refuses any static path with a dot-prefixed segment. STATIC_DIR
// defaults to ".", so a server started from the repo root would otherwise
// serve .git/ (config, refs, objects) and any .env file sitting beside it.
// This checks the URL path only: dot-directories (including .well-known) are
// denied wholesale, and a symlinked target is not resolved - keep links to
// dotfiles out of STATIC_DIR.
func denyDotfiles(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/.") {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) handleStarmap(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	repo := r.URL.Query().Get("repo")
	if !repoRe.MatchString(repo) {
		writeErr(w, &apiError{status: 400, code: "bad_repo", msg: "repo must be owner/name"})
		return
	}
	// A configured GitHub token may access private repositories. Do not allow
	// a shared CDN to cache their contracts across visitors.
	w.Header().Set("Cache-Control", "private, no-store")

	blob, err := s.build(r.Context(), repo)
	if err != nil {
		if ae, ok := err.(*apiError); ok {
			writeErr(w, ae)
		} else {
			writeErr(w, &apiError{status: 500, code: "internal", msg: err.Error()})
		}
		return
	}
	_, _ = w.Write(blob)
}

// build returns the contract JSON for a repo, serving from cache when the HEAD
// sha still matches, otherwise rebuilding from git and repopulating the cache.
func (s *server) build(ctx context.Context, repo string) ([]byte, error) {
	return s.buildWithProgress(ctx, repo, func(progressEvent) {})
}

// buildWithProgress is build() instrumented with an emit callback. Every phase
// reports honest telemetry (real counts, cache state) so the streaming endpoint
// can drive the analysis UI; the non-streaming path passes a no-op emit. emit
// must be safe to call concurrently - the streaming handler serialises it
// behind a mutex.
func (s *server) buildWithProgress(ctx context.Context, repo string, emit emitFn) ([]byte, error) {
	repo = canonicalRepo(repo)
	// One `git ls-remote --symref` answers both "what is the default branch"
	// and "what is HEAD". It runs over the git transport, so unlike the REST
	// call it replaces it costs no token quota - which matters because this
	// runs on every request, cache hit or miss.
	emit(progressEvent{Phase: "resolve", Status: "start"})
	headSHA, defaultBranch, err := s.git.remoteHead(ctx, repo)
	if err != nil {
		return nil, err
	}
	emit(progressEvent{Phase: "resolve", Status: "done", DefaultBranch: defaultBranch})

	emit(progressEvent{Phase: "head", Status: "start"})
	key := repo + "@" + headSHA + "#" + paramsVersion
	if blob, ok := s.cache.Get(key); ok {
		log.Printf("cache HIT  %s", key)
		emit(progressEvent{Phase: "head", Status: "done",
			CommitTotal: cachedTotal(blob), HeadSha: short(headSHA), Cached: true})
		emit(progressEvent{Phase: "history", Status: "skip"})
		emit(progressEvent{Phase: "map", Status: "skip"})
		return blob, nil
	}
	buildCtx := ctx
	if s.lease != nil {
		if s.lease.knownTooLarge(repo) {
			return nil, errRepoTooLarge
		}
		var release func()
		for attempt := 0; release == nil; attempt++ {
			r, acquired := s.lease.tryBuildLease(key)
			if acquired {
				release = r
				break
			}
			// Another instance is building this exact repo@HEAD. Keep the
			// stream alive while it works: the wait can last as long as a
			// cold build does.
			stop := keepalive(emit)
			blob, err := s.waitForBuild(ctx, repo, key)
			stop()
			if errors.Is(err, errLeaseReleased) {
				if attempt == 0 {
					continue // the winner failed: build it here, once
				}
				err = errBusy
			}
			if err != nil {
				return nil, err
			}
			emit(progressEvent{Phase: "head", Status: "done", CommitTotal: cachedTotal(blob), HeadSha: short(headSHA), Cached: true})
			emit(progressEvent{Phase: "history", Status: "skip"})
			emit(progressEvent{Phase: "map", Status: "skip"})
			return blob, nil
		}
		defer release()
		// gitSource.snapshot intentionally finishes an in-flight clone after
		// its initiating client disconnects, to share work with other callers.
		// Keep the Redis lease until that detached work has completed and the
		// contract has been cached; otherwise another instance can reclone it.
		buildCtx = context.WithoutCancel(ctx)
		// A competing instance may have finished between our first read and
		// lease acquisition. Avoid repeating an already-completed clone.
		if blob, ok := s.cache.Get(key); ok {
			emit(progressEvent{Phase: "head", Status: "done", CommitTotal: cachedTotal(blob), HeadSha: short(headSHA), Cached: true})
			emit(progressEvent{Phase: "history", Status: "skip"})
			emit(progressEvent{Phase: "map", Status: "skip"})
			return blob, nil
		}
	}
	// Only a build that is really about to clone spends the visitor's
	// cold-build budget; hits and waits on another instance's build do not.
	if err := s.allowBuild(ctx); err != nil {
		return nil, err
	}
	log.Printf("cache MISS %s (building from git)", key)
	// The commit total now comes from the clone rather than from a REST header,
	// so this frame carries the sha and a second head frame below fills in the
	// count once it is actually known.
	emit(progressEvent{Phase: "head", Status: "done", HeadSha: short(headSHA), Cached: false})

	// The whole history - commit graph, tags, branches, merged PRs - comes out
	// of one blob-less bare clone. Concurrent requests for the same repo at the
	// same head share this clone instead of each starting their own.
	emit(progressEvent{Phase: "history", Status: "start"})
	stop := keepalive(emit)
	snap, err := s.git.snapshot(buildCtx, repo, headSHA, defaultBranch)
	stop()
	if err != nil {
		if errors.Is(err, errRepoTooLarge) && s.lease != nil {
			s.lease.rememberTooLarge(repo) // spare the other instances the download
		}
		return nil, err
	}
	if len(snap.Commits) == 0 {
		return nil, errEmptyRepo
	}
	emit(progressEvent{Phase: "head", Status: "done",
		CommitTotal: snap.Total, HeadSha: short(headSHA), Cached: false})
	emit(progressEvent{Phase: "history", Status: "progress", Resource: "commits", Count: snap.Total})
	emit(progressEvent{Phase: "history", Status: "progress", Resource: "tags", Count: len(snap.TagSHA)})
	emit(progressEvent{Phase: "history", Status: "progress", Resource: "branches", Count: len(snap.Branches)})

	// Merged PRs are the one thing git cannot always answer, so they are the one
	// thing still worth a token. A merge commit carries everything (branch name
	// and exact commit count); a squash or rebase merge does not, and only the
	// API still remembers. That fallback costs at most 9 calls per cold build -
	// against the 200-1,400 the REST commit walk used to cost - and it is
	// skipped entirely when no token is configured, so the default deployment
	// stays at zero REST calls and labels the PR honestly as squashed.
	pulls := snap.PRs
	if s.gh.tokens.size() > 0 && !hasSizedPR(pulls) {
		// The fallback is up to 9 serial REST calls; bound the whole phase so
		// one stalled call cannot hold the build for the sum of their worst
		// cases (CLONE_TIMEOUT does not cover it - the clone is already done).
		pctx, cancel := context.WithTimeout(buildCtx, 60*time.Second)
		if rest, err := s.gh.mergedPulls(pctx, repo); err == nil && len(rest) > 0 {
			pulls = rest
		}
		cancel()
	}
	emit(progressEvent{Phase: "history", Status: "progress", Resource: "pulls", Count: len(pulls)})
	emit(progressEvent{Phase: "history", Status: "done"})

	emit(progressEvent{Phase: "map", Status: "start"})
	main := buildMain(snap.Commits, snap.TagSHA)
	scale := computeScale(len(main))

	headDate := float64(snap.Commits[0].Committed.Unix())
	genesisDate := float64(snap.Commits[len(snap.Commits)-1].Committed.Unix())
	branches := buildBranches(pulls, snap.Branches, genesisDate, headDate)

	c := Contract{
		Repo:        repo,
		HeadSHA:     short(headSHA),
		CommitTotal: snap.Total,
		Scale:       scale,
		Main:        main,
		Branches:    branches,
	}
	blob, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	emit(progressEvent{Phase: "map", Status: "done", Stars: len(main), Constellations: len(branches)})
	s.cache.Put(key, blob, 15*time.Minute)
	return blob, nil
}

// progressEvent is one NDJSON frame on the streaming endpoint. Fields are
// omitempty so each phase only carries the keys it actually populates; the final
// frame is phase:"result" carrying the raw contract, and phase:"error" carries a
// code+message. Mirror this shape in the web client's stream reader.
type progressEvent struct {
	Phase          string          `json:"phase"`
	Status         string          `json:"status,omitempty"`
	DefaultBranch  string          `json:"defaultBranch,omitempty"`
	CommitTotal    int             `json:"commitTotal,omitempty"`
	HeadSha        string          `json:"headSha,omitempty"`
	Cached         bool            `json:"cached,omitempty"`
	Resource       string          `json:"resource,omitempty"`
	Count          int             `json:"count,omitempty"`
	Stars          int             `json:"stars,omitempty"`
	Constellations int             `json:"constellations,omitempty"`
	Code           string          `json:"code,omitempty"`
	Message        string          `json:"message,omitempty"`
	Contract       json.RawMessage `json:"contract,omitempty"`
}

type emitFn func(progressEvent)

// handleStarmapStream streams NDJSON progress frames while building the contract,
// then a final result (or error) frame. It always responds 200: once the first
// frame is flushed the status is committed, so failures are reported in-band as a
// phase:"error" frame rather than an HTTP status.
func (s *server) handleStarmapStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no") // don't let a proxy buffer the stream

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	controller := http.NewResponseController(w)
	enc := json.NewEncoder(w)
	var mu sync.Mutex
	emit := func(ev progressEvent) {
		mu.Lock()
		defer mu.Unlock()
		if ctx.Err() != nil {
			return
		}
		// A client that stopped reading must not hold a clone worker or a
		// goroutine indefinitely. Each flush has an independent deadline.
		_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := enc.Encode(ev); err != nil {
			cancel()
			return
		}
		if err := controller.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
			cancel()
		}
	}

	repo := r.URL.Query().Get("repo")
	if !repoRe.MatchString(repo) {
		emit(progressEvent{Phase: "error", Code: "bad_repo", Message: "repo must be owner/name"})
		return
	}

	blob, err := s.buildWithProgress(ctx, repo, emit)
	if err != nil {
		code, msg := "internal", err.Error()
		if ae, ok := err.(*apiError); ok {
			code, msg = ae.code, ae.msg
		}
		emit(progressEvent{Phase: "error", Code: code, Message: msg})
		return
	}
	emit(progressEvent{Phase: "result", Contract: json.RawMessage(blob)})
}

// hasSizedPR reports whether git recovered a PR with its real commit count,
// which only a true merge commit preserves.
func hasSizedPR(prs []prMerge) bool {
	for _, p := range prs {
		if p.Commits > 0 {
			return true
		}
	}
	return false
}

// cachedTotal reads the commit count back out of a cached contract, so a cache
// hit reports the same telemetry a fresh build does without touching git.
func cachedTotal(blob []byte) int {
	var c struct {
		CommitTotal int `json:"commitTotal"`
	}
	_ = json.Unmarshal(blob, &c)
	return c.CommitTotal
}

// keepalive emits an inert progress frame every few seconds while the clone
// runs. Cloning a large repo can outlast the idle timeout of any proxy between
// this server and the browser; these frames carry no resource, so the client's
// reducer ignores them and they exist purely to keep the stream flowing.
// stop() blocks until the emitter has stopped, so no frame can be written after
// the handler has returned.
func keepalive(emit emitFn) (stop func()) {
	done, exited := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(exited)
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				emit(progressEvent{Phase: "history", Status: "progress"})
			}
		}
	}()
	return sync.OnceFunc(func() {
		close(done)
		<-exited
	})
}

func writeErr(w http.ResponseWriter, e *apiError) {
	if e.status == http.StatusServiceUnavailable {
		w.Header().Set("Retry-After", "20")
	}
	if e.retryAfter > 0 {
		w.Header().Set("Retry-After", retrySeconds(e.retryAfter))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": e.code, "message": e.msg})
}

// README embeds cannot execute Three.js. The animated GIF captures the same
// contract's genesis-to-HEAD growth as a small, deterministic image.
func NewGalaxyGIFHandler(build func(context.Context, string) ([]byte, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeErr(w, &apiError{status: 405, code: "method_not_allowed", msg: "GET or HEAD required"})
			return
		}
		repos := r.URL.Query()["repo"]
		if len(repos) != 1 || !badgeValidRepo(repos[0]) {
			writeErr(w, &apiError{status: 400, code: "bad_repo", msg: "repo must be owner/name"})
			return
		}
		repo := canonicalRepo(repos[0])
		blob, err := build(r.Context(), repo)
		if err != nil {
			var ae *apiError
			if errors.As(err, &ae) {
				writeErr(w, ae)
			} else {
				writeErr(w, &apiError{status: 500, code: "internal", msg: "badge source unavailable"})
			}
			return
		}
		var contract Contract
		if json.Unmarshal(blob, &contract) != nil || contract.Repo != repo {
			writeErr(w, &apiError{status: 502, code: "bad_contract", msg: "invalid cached contract"})
			return
		}
		data, err := renderGalaxyGIF(contract)
		if err != nil {
			writeErr(w, &apiError{status: 422, code: "bad_contract", msg: "invalid star map contract"})
			return
		}
		sum := sha256.Sum256(data)
		etag := fmt.Sprintf("\"%x\"", sum[:12])
		w.Header().Set("Content-Type", "image/gif")
		w.Header().Set("Cache-Control", "public, max-age=300, stale-while-revalidate=3600")
		w.Header().Set("ETag", etag)
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		if r.Method == http.MethodGet {
			_, _ = w.Write(data)
		}
	}
}

func (s *server) handleBadgeGIF(w http.ResponseWriter, r *http.Request) {
	NewGalaxyGIFHandler(s.build)(w, r)
}

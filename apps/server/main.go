package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"regexp"
	"sync"
	"time"
)

func getenv(k string) string { return os.Getenv(k) }

type server struct {
	gh    *ghClient  // residual REST: merged PRs only
	git   *gitSource // the history itself
	cache Cache
}

var repoRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

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

	var cache Cache
	if c, err := newSQLiteCache(dbPath); err != nil {
		log.Printf("cache: init failed (%v) - running without cache", err)
	} else {
		cache = c
	}
	tokens := newTokenPool()
	git := newGitSource(tokens)
	s := &server{gh: newGHClient(tokens), git: git, cache: bestEffort{inner: cache}}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/api/starmap", s.handleStarmap)
	mux.HandleFunc("/api/starmap/stream", s.handleStarmapStream)
	mux.Handle("/", http.FileServer(http.Dir(staticDir)))

	log.Printf("stellar-server listening on :%s (static=%s, cache=%s, mirrors=%s, tokens=%d)",
		port, staticDir, dbPath, git.dir, tokens.size())
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatal(err)
	}
}

func (s *server) handleStarmap(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	repo := r.URL.Query().Get("repo")
	if !repoRe.MatchString(repo) {
		writeErr(w, &apiError{status: 400, code: "bad_repo", msg: "repo must be owner/name"})
		return
	}

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
	snap, err := s.git.snapshot(ctx, repo, headSHA, defaultBranch)
	stop()
	if err != nil {
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
		if rest, err := s.gh.mergedPulls(ctx, repo); err == nil && len(rest) > 0 {
			pulls = rest
		}
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
	s.cache.Put(key, blob, 24*time.Hour)
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

	flusher, _ := w.(http.Flusher)
	enc := json.NewEncoder(w)
	var mu sync.Mutex
	emit := func(ev progressEvent) {
		mu.Lock()
		defer mu.Unlock()
		_ = enc.Encode(ev) // Encode appends '\n' -> one NDJSON line per frame
		if flusher != nil {
			flusher.Flush()
		}
	}

	repo := r.URL.Query().Get("repo")
	if !repoRe.MatchString(repo) {
		emit(progressEvent{Phase: "error", Code: "bad_repo", Message: "repo must be owner/name"})
		return
	}

	blob, err := s.buildWithProgress(r.Context(), repo, emit)
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
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": e.code, "message": e.msg})
}

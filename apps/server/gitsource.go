package main

// Data acquisition for the star map.
//
// The heavy history - commit graph, branches, tags - comes from a blob-less
// bare clone over the git transport, which is a *separate* budget from the REST
// API's 5,000 req/hr. A public repo is cloned anonymously, so it costs zero
// token quota no matter how many commits it has (the old REST path spent up to
// ~1,400 calls on a single large repo). The REST client keeps only what git
// cannot answer: merged-PR metadata when the repo's history has no PR
// references, and auth for private repos.

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sync/singleflight"
)

var (
	errCloneTimeout = &apiError{status: 504, code: "clone_timeout",
		msg: "repository took too long to read, try again later"}
	errRepoTooLarge = &apiError{status: 413, code: "repo_too_large",
		msg: "repository is too large to chart"}
	errGitUnavailable = &apiError{status: 500, code: "git_unavailable",
		msg: "git is not available on this server"}
)

// How much of the streamed history we hold on to. git log is read as a stream
// and everything outside these bounds is discarded, so peak memory is O(stars)
// rather than O(commits) - torvalds/linux has ~1.4M commits and must not cost
// 1.4M structs per request.
const (
	// PR merge commits collected while streaming (newest first). buildBranches
	// only ever renders a handful; the surplus is headroom for dedupe.
	prCandidates = 32
	// Live branches measured against HEAD. Each costs 3 cheap local git calls.
	branchCandidates = 12
	// Tags kept, newest first. Mirrors the old REST `tags?per_page=100` window
	// so a tag-heavy repo doesn't turn most of its trunk into release stars.
	tagWindow = 100
)

// ---- parsed history (what transform.go consumes) ----

// commitRec is one commit from `git log`. Replaces the REST ghCommit shape;
// only the fields the transform actually reads are kept.
type commitRec struct {
	SHA       string
	Parents   []string
	Committed time.Time
}

// prMerge is a merged pull request recovered from the commit graph: either a
// real merge commit ("Merge pull request #N from owner/ref") or a squash-merge
// subject ("Some title (#N)"). Commits is 0 when the PR was squashed and its
// original commit count no longer exists in the history.
type prMerge struct {
	Number   int
	Ref      string
	Commits  int
	MergedAt time.Time
	sha      string // the merge commit, used to count the PR's commits
	merged   bool   // true for a real merge commit (2 parents), false for a squash
}

// branchRec is a live non-default branch measured against HEAD.
type branchRec struct {
	Name    string
	AheadBy int
	BaseAt  time.Time
}

// repoSnapshot is everything one build needs from git.
type repoSnapshot struct {
	Total    int               // commits reachable from HEAD
	Commits  []commitRec       // newest-first, reduced to the sampled + milestone set
	TagSHA   map[string]string // short sha -> tag name
	PRs      []prMerge
	Branches []branchRec
}

// ---- the source ----

type gitSource struct {
	dir      string     // mirror cache root
	home     string     // isolated HOME so no user gitconfig is ever read
	tokens   *tokenPool // private-repo auth only; never logged
	sem      chan struct{}
	flight   singleflight.Group
	timeout  time.Duration
	probe    time.Duration // ceiling for the per-request HEAD freshness probe
	maxBytes int64         // per-repo cap, enforced while cloning
	diskCap  int64         // total mirror-cache budget
	ttl      time.Duration

	gate     *mirrorGate // per-directory use vs sweep arbitration
	sweeping sync.Mutex  // one eviction pass at a time
}

func newGitSource(tokens *tokenPool) *gitSource {
	dir := strings.TrimSpace(getenv("CLONE_DIR"))
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "stellar-repos")
	}
	home := filepath.Join(dir, ".home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		log.Printf("git: clone dir %s not writable (%v) - every build will fail", dir, err)
	}
	g := &gitSource{
		dir:      dir,
		home:     home,
		tokens:   tokens,
		sem:      make(chan struct{}, envInt("CLONE_CONCURRENCY", 4)),
		timeout:  envDur("CLONE_TIMEOUT", 10*time.Minute),
		probe:    envDur("CLONE_PROBE_TIMEOUT", 3*time.Minute),
		maxBytes: int64(envInt("CLONE_MAX_MB", 4096)) << 20,
		diskCap:  int64(envInt("CLONE_DISK_MB", 20480)) << 20,
		ttl:      envDur("CLONE_TTL", 7*24*time.Hour),
		gate:     newMirrorGate(),
	}
	return g
}

// ---- remote head (git transport, zero REST cost) ----

// remoteHead returns the repo's HEAD sha and default branch name in a single
// `git ls-remote --symref` round trip. This is the freshness probe and cache
// key source: it replaces the REST call that used to cost quota on every hit.
func (g *gitSource) remoteHead(ctx context.Context, repo string) (headSHA, defaultBranch string, err error) {
	// The probe runs on every request, cache hit or miss, so it must not be
	// able to hang a handler behind a stalled connection: git's HTTP transport
	// has no low-speed abort of its own, and without a deadline a blackholed
	// socket can pin the request for the kernel's retransmit budget (many
	// minutes), or indefinitely while the client waits. Bounded well below
	// CLONE_TIMEOUT because a probe that slow has already failed.
	ctx, cancel := context.WithTimeout(ctx, g.probe)
	defer cancel()

	// Anonymous first: a public repo is probed with no token at all, so a
	// stale or revoked credential cannot turn a working public repo into a
	// 404. Only an anonymous miss (how a private repo presents itself)
	// retries with credentials.
	out, err := g.run(ctx, "", "ls-remote", "--symref", repoURL(repo), "HEAD")
	if g.retryAuthed(err) {
		// If the credentialed retry fails too, the anonymous error is the one
		// reported: it names the real problem (an IP rate limit, say) better
		// than the credential error that replaced it.
		if out2, err2 := g.runEnv(ctx, true, "", "ls-remote", "--symref", repoURL(repo), "HEAD"); err2 == nil {
			out, err = out2, nil
		}
	}
	if err != nil {
		return "", "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "ref:"):
			// "ref: refs/heads/main\tHEAD"
			f := strings.Fields(strings.TrimPrefix(line, "ref:"))
			if len(f) > 0 {
				defaultBranch = strings.TrimPrefix(f[0], "refs/heads/")
			}
		case strings.HasSuffix(line, "\tHEAD"):
			headSHA = strings.TrimSuffix(line, "\tHEAD")
		}
	}
	if headSHA == "" {
		// A reachable repo with no commits advertises no refs at all.
		return "", "", errEmptyRepo
	}
	if defaultBranch == "" {
		defaultBranch = "main"
	}
	return headSHA, defaultBranch, nil
}

// ---- snapshot ----

// snapshot returns the parsed history for repo at headSHA. Concurrent requests
// for the same repo+head share one clone and one parse: the second caller
// blocks on the first instead of starting a duplicate clone.
func (g *gitSource) snapshot(ctx context.Context, repo, headSHA, defaultBranch string) (*repoSnapshot, error) {
	return g.snapshotVia(ctx, repo, headSHA, defaultBranch, g.buildSnapshot)
}

// snapshotVia is snapshot with the build step injectable, so a test can pin
// that the panic guard really wraps the shared build - if it ever stops doing
// so, a panic escapes singleflight into a fresh goroutine and kills the
// process (see guardedSnapshot).
func (g *gitSource) snapshotVia(ctx context.Context, repo, headSHA, defaultBranch string,
	build func(context.Context, string, string, string) (*repoSnapshot, error)) (*repoSnapshot, error) {
	ch := g.flight.DoChan(repo+"@"+headSHA, func() (any, error) {
		// Detached from whichever caller happened to start it: one browser tab
		// closing must not cancel a clone every other waiter depends on, and a
		// clone is worth finishing anyway because it warms the mirror. The
		// clone timeout and the semaphore still bound it.
		snap, err := guardedSnapshot(repo, func() (*repoSnapshot, error) {
			return build(context.WithoutCancel(ctx), repo, headSHA, defaultBranch)
		})
		return snap, err
	})
	select {
	case res := <-ch:
		if res.Err != nil {
			return nil, res.Err
		}
		return res.Val.(*repoSnapshot), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// guardedSnapshot converts a panic inside a shared build into an ordinary
// error. singleflight re-raises a panic in a fresh goroutine when callers are
// attached to it, where net/http's per-connection recover cannot catch it - so
// without this, one bad input would take down every request in flight. The
// detached build therefore contains its own failures.
func guardedSnapshot(repo string, build func() (*repoSnapshot, error)) (snap *repoSnapshot, err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("git: panic building %s: %v", repo, r)
			snap, err = nil, &apiError{status: 500, code: "internal",
				msg: "internal error while building repository"}
		}
	}()
	return build()
}

func (g *gitSource) buildSnapshot(ctx context.Context, repo, headSHA, defaultBranch string) (*repoSnapshot, error) {
	dir, err := g.mirrorPath(repo)
	if err != nil {
		return nil, err
	}

	// One build at a time per mirror, held from before ensureMirror until the
	// parse is done: a fetch (or a failed fetch's RemoveAll) must never race
	// another build reading the same directory, and the sweep must see the
	// mirror as in use for the whole span (see mirrorGate).
	g.gate.acquire(dir)
	defer g.gate.release(dir)

	// Bound how many clones run at once: each one is network + disk heavy, and
	// an unbounded fan-out would thrash both.
	select {
	case g.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-g.sem }()

	// This timeout wraps the clone *and* the parse, which is what makes an idle
	// mtime a safe eviction signal (see sweep).
	cctx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()

	if err := g.ensureMirror(cctx, repo, dir); err != nil {
		return nil, err
	}
	defer func() { go g.sweep() }() // keep the mirror cache inside its disk budget

	snap, err := g.parse(cctx, dir, headSHA, defaultBranch)
	if err != nil {
		return nil, err
	}
	return snap, nil
}

// ensureMirror clones the repo blob-less on first use and incrementally fetches
// it afterwards, so a repeat build pays for new objects only. The clone lands in
// a temp dir and is renamed into place, so an interrupted clone can never be
// mistaken for a usable mirror.
func (g *gitSource) ensureMirror(ctx context.Context, repo, dir string) error {
	if _, err := os.Stat(filepath.Join(dir, "HEAD")); err == nil {
		log.Printf("git: fetch %s", repo)
		// Lease the mirror BEFORE the network read: sweep treats the mtime as
		// "in use since", and a slow fetch must not look idle while it runs.
		g.touch(dir)
		args := []string{"fetch", "--filter=tree:0", "--prune",
			repoURL(repo), "+refs/heads/*:refs/heads/*", "+refs/tags/*:refs/tags/*"}
		_, err := g.runCapped(ctx, false, dir, dir, args...)
		if g.retryAuthed(err) {
			// Report the anonymous failure if the retry fails too (see remoteHead).
			if _, err2 := g.runCapped(ctx, true, dir, dir, args...); err2 == nil {
				err = nil
			}
		}
		if err == nil {
			g.touch(dir) // LRU stamp: last use, not last write
			return nil
		}
		// A corrupt or half-written mirror must not wedge this repo forever.
		log.Printf("git: refetch failed for %s, recloning", repo)
		_ = os.RemoveAll(dir)
	}

	log.Printf("git: clone %s (blob-less bare)", repo)
	tmp := dir + ".tmp" + strconv.FormatInt(time.Now().UnixNano(), 36)
	args := []string{"clone", "--bare", "--filter=tree:0", repoURL(repo), tmp}
	_, err := g.runCapped(ctx, false, "", tmp, args...)
	if g.retryAuthed(err) {
		// The anonymous attempt may have left partial state in tmp, and git
		// clone refuses a non-empty destination: clear it before the retry.
		// If the retry fails too, the anonymous error is the one reported.
		_ = os.RemoveAll(tmp)
		if _, err2 := g.runCapped(ctx, true, "", tmp, args...); err2 == nil {
			err = nil
		}
	}
	if err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	if err := os.Rename(tmp, dir); err != nil {
		_ = os.RemoveAll(tmp)
		return fmt.Errorf("install mirror: %w", err)
	}
	g.touch(dir)
	return nil
}

// touch stamps a mirror's mtime, the lease the sweep checks before evicting.
func (g *gitSource) touch(dir string) {
	now := time.Now()
	_ = os.Chtimes(dir, now, now)
}

// parse reads the mirror into a snapshot. Everything here is local: no network,
// no REST, no token.
func (g *gitSource) parse(ctx context.Context, dir, headSHA, defaultBranch string) (*repoSnapshot, error) {
	out, err := g.run(ctx, dir, "rev-list", "--count", headSHA)
	if err != nil {
		return nil, err
	}
	total, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || total == 0 {
		return nil, errEmptyRepo
	}

	tagSHA, err := g.tags(ctx, dir)
	if err != nil {
		return nil, err
	}

	commits, prs, err := g.history(ctx, dir, headSHA, total, tagSHA)
	if err != nil {
		return nil, err
	}
	if len(commits) == 0 {
		return nil, errEmptyRepo
	}

	prs = g.countPRCommits(ctx, dir, prs)
	skip := map[string]bool{defaultBranch: true}
	for _, p := range prs {
		skip[p.Ref] = true
	}
	branches := g.branches(ctx, dir, headSHA, skip)

	return &repoSnapshot{
		Total:    total,
		Commits:  commits,
		TagSHA:   tagSHA,
		PRs:      prs,
		Branches: branches,
	}, nil
}

// tags returns short-sha -> tag name for the newest `tagWindow` tags.
// Annotated tags are peeled to the commit they point at.
func (g *gitSource) tags(ctx context.Context, dir string) (map[string]string, error) {
	out, err := g.run(ctx, dir, "for-each-ref", "--sort=-creatordate",
		"--count="+strconv.Itoa(tagWindow),
		"--format=%(refname:short)%09%(objectname)%09%(*objectname)", "refs/tags")
	if err != nil {
		return nil, err
	}
	tags := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 3 || f[0] == "" {
			continue
		}
		sha := f[2] // peeled commit for annotated tags
		if sha == "" {
			sha = f[1] // lightweight tag: the ref is the commit
		}
		if sha != "" {
			tags[short(sha)] = f[0]
		}
	}
	return tags, nil
}

var (
	// GitHub's merge-commit subject for a merged PR.
	prMergeRe = regexp.MustCompile(`^Merge pull request #(\d+) from (\S+)`)
	// GitHub's squash-merge subject: the PR title with a trailing "(#123)".
	prSquashRe = regexp.MustCompile(`^(.+?)\s+\(#(\d+)\)$`)
)

// history streams `git log` newest-first and keeps only what the contract can
// possibly need: the evenly spaced sample the transform will draw from, every
// commit a tag points at, and the newest PR merges. The full log is never held
// in memory - a 1.4M-commit repo streams through in constant space.
func (g *gitSource) history(ctx context.Context, dir, headSHA string, total int,
	tagSHA map[string]string) (commits []commitRec, prs []prMerge, err error) {

	keep := make(map[int]bool, maxStars)
	for _, p := range evenIndices(total, maxStars) {
		keep[p-1] = true // evenIndices is 1-based
	}

	cmd, stdout, err := g.start(ctx, dir, "log", "--format=%H%x09%P%x09%ct%x09%s", headSHA)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		_ = stdout.Close()
		// A process can produce valid-looking partial output before exiting
		// nonzero (corrupt history, failed lazy fetch, etc.). Never cache a
		// truncated history as a successful contract.
		if waitErr := cmd.Wait(); waitErr != nil && err == nil {
			commits, prs, err = nil, nil, gitError(ctx, waitErr, "")
		}
	}()

	var (
		last commitRec
		i    int
	)
	r := bufio.NewReaderSize(stdout, 64<<10)
	for {
		line, rerr := r.ReadString('\n')
		if line = strings.TrimRight(line, "\n"); line != "" {
			f := strings.SplitN(line, "\t", 4)
			if len(f) == 4 {
				secs, _ := strconv.ParseInt(f[2], 10, 64)
				rec := commitRec{
					SHA:       f[0],
					Committed: time.Unix(secs, 0).UTC(),
				}
				if f[1] != "" {
					rec.Parents = strings.Fields(f[1])
				}
				if keep[i] || tagSHA[short(rec.SHA)] != "" {
					commits = append(commits, rec)
				}
				if len(prs) < prCandidates {
					if pr, ok := parsePR(f[3], rec); ok {
						prs = append(prs, pr)
					}
				}
				last = rec
				i++
			}
		}
		if rerr != nil {
			if rerr != io.EOF {
				return nil, nil, rerr
			}
			break
		}
	}

	// The trunk's oldest star must be the true genesis commit even if the
	// rev-list count and the log disagree (shallow edges, replace refs).
	if last.SHA != "" && (len(commits) == 0 || commits[len(commits)-1].SHA != last.SHA) {
		commits = append(commits, last)
	}
	return commits, prs, nil
}

// parsePR recognises a merged pull request from a commit subject.
func parsePR(subject string, c commitRec) (prMerge, bool) {
	if m := prMergeRe.FindStringSubmatch(subject); m != nil {
		n, _ := strconv.Atoi(m[1])
		ref := m[2]
		// "owner/branch-name" -> "branch-name" (a branch name may contain "/").
		if i := strings.Index(ref, "/"); i >= 0 {
			ref = ref[i+1:]
		}
		if n > 0 && ref != "" {
			return prMerge{
				Number: n, Ref: ref, MergedAt: c.Committed,
				sha: c.SHA, merged: len(c.Parents) > 1,
			}, true
		}
	}
	if m := prSquashRe.FindStringSubmatch(subject); m != nil {
		n, _ := strconv.Atoi(m[2])
		// A squash merge leaves no branch ref, so the PR title is the honest
		// label. Trimmed to branch-name length so the legend stays balanced.
		title := strings.TrimSpace(m[1])
		if len(title) > 32 {
			title = strings.TrimSpace(title[:32]) + "…"
		}
		if n > 0 && title != "" {
			return prMerge{Number: n, Ref: title, MergedAt: c.Committed, sha: c.SHA}, true
		}
	}
	return prMerge{}, false
}

// prKey is the dedupe identity of a PR. The number wins when known: two
// distinct PRs can share a squash-merge title (the Ref), and collapsing them
// would drop one asterism from the legend.
func prKey(p prMerge) string {
	if p.Number > 0 {
		return "#" + strconv.Itoa(p.Number)
	}
	return p.Ref
}

// countPRCommits keeps the newest PRs (deduped by PR number when known, capped
// at what the legend can show) and fills in the exact commit count for real
// merge commits:
// everything reachable from the merged branch but not from the trunk it landed
// on. Squashed PRs keep Commits == 0 - their original commits no longer exist,
// and inventing a number would put a fabricated count on the star map.
func (g *gitSource) countPRCommits(ctx context.Context, dir string, prs []prMerge) []prMerge {
	seen := map[string]bool{}
	out := make([]prMerge, 0, branchCap)
	for _, p := range prs {
		if len(out) >= branchCap {
			break
		}
		key := prKey(p)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, p)
	}

	var wg sync.WaitGroup
	for i := range out {
		if !out[i].merged {
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			n, err := g.countInt(ctx, dir, "rev-list", "--count",
				out[i].sha+"^1.."+out[i].sha+"^2")
			if err == nil && n > 0 {
				out[i].Commits = n
			}
		}(i)
	}
	wg.Wait()
	return out
}

// branches measures live non-default branches against HEAD: how far ahead they
// are and when they forked. This is the local equivalent of the REST
// `compare/base...head` call the old path spent one request on per branch.
func (g *gitSource) branches(ctx context.Context, dir, headSHA string, skip map[string]bool) []branchRec {
	out, err := g.run(ctx, dir, "for-each-ref", "--sort=-committerdate",
		"--format=%(refname:short)", "refs/heads")
	if err != nil {
		return nil
	}
	var recs []branchRec
	for _, name := range strings.Split(string(out), "\n") {
		if len(recs) >= branchCandidates {
			break
		}
		name = strings.TrimSpace(name)
		if name == "" || skip[name] {
			continue
		}
		ahead, err := g.countInt(ctx, dir, "rev-list", "--count", headSHA+".."+name)
		if err != nil || ahead <= 0 {
			continue
		}
		base, err := g.run(ctx, dir, "merge-base", headSHA, name)
		if err != nil {
			continue
		}
		at, err := g.countInt(ctx, dir, "log", "-1", "--format=%ct", strings.TrimSpace(string(base)))
		if err != nil {
			continue
		}
		recs = append(recs, branchRec{
			Name:    name,
			AheadBy: ahead,
			BaseAt:  time.Unix(int64(at), 0).UTC(),
		})
	}
	return recs
}

func (g *gitSource) countInt(ctx context.Context, dir string, args ...string) (int, error) {
	out, err := g.run(ctx, dir, args...)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

// ---- process plumbing ----

// repoURL is always the plain public URL: the token, when there is one, travels
// in the environment (see gitEnv) so it never appears in argv, which is world
// readable through /proc.
func repoURL(repo string) string { return "https://github.com/" + repo + ".git" }

// gitEnv is a deliberately minimal environment: no user config, no system
// config, no interactive prompt, no credential helper. A repo URL is
// attacker-controlled input, so git gets nothing it could be talked into using.
// Credentials are only layered in when authed is true: network callers start
// anonymous and retry with auth only on a not-found (see retryAuthed).
func (g *gitSource) gitEnv(authed bool) []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + g.home,
		"GIT_TERMINAL_PROMPT=0",    // never block on a credential prompt
		"GIT_CONFIG_NOSYSTEM=1",    // ignore /etc/gitconfig
		"GIT_ASKPASS=",             // no GUI/helper credential path
		"GIT_PROTOCOL_FROM_USER=0", // treat the URL as untrusted input
		"GIT_ALLOW_PROTOCOL=https",
	}
	if !authed {
		return env
	}
	if t := g.tokens.next(); t != "" {
		// Private-repo auth. Passed as config through the environment (git
		// >= 2.31) rather than in the URL or argv, so the token is never
		// visible in process listings and never lands in a log line.
		basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + t))
		env = append(env,
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=http.extraHeader",
			"GIT_CONFIG_VALUE_0=Authorization: Basic "+basic,
		)
	}
	return env
}

func (g *gitSource) command(ctx context.Context, dir string, authed bool, args ...string) *exec.Cmd {
	full := append([]string{
		"-c", "core.hooksPath=" + filepath.Join(g.home, "no-hooks"), // never run repo-supplied hooks
		"-c", "protocol.version=2",
		"-c", "advice.detachedHead=false",
	}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = dir
	cmd.Env = g.gitEnv(authed)
	// Kill the whole process group on timeout: git spawns git-remote-https,
	// which would otherwise keep downloading after the parent is gone.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return os.ErrProcessDone
	}
	// WaitDelay bounds the post-kill pipe drain: Wait reads stdout until EOF,
	// which a lingering subprocess could otherwise hold open past the context
	// deadline and past the process-group kill.
	cmd.WaitDelay = 10 * time.Second
	return cmd
}

// retryAuthed reports whether a failed anonymous network attempt is worth
// repeating with credentials. A private repo looks exactly like a missing one
// to anonymous git (404 "not found"), so that is the main failure a token can
// fix. An anonymous rate limit is the other: GitHub's git transport limits the
// caller's IP, and an authenticated request has its own quota, so a token can
// lift it too. Everything else - a real 404, a network fault, a server error -
// fails the same way with or without auth.
func (g *gitSource) retryAuthed(err error) bool {
	return err != nil && g.tokens.size() > 0 &&
		(errors.Is(err, errNotFound) || errors.Is(err, errRateLimit))
}

// run executes a git command and returns stdout.
func (g *gitSource) run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return g.runEnv(ctx, false, dir, args...)
}

// runEnv executes a git command, optionally with credentials, and returns
// stdout. Local commands never need auth; network callers start anonymous and
// retry with credentials only when retryAuthed says a token could help.
func (g *gitSource) runEnv(ctx context.Context, authed bool, dir string, args ...string) ([]byte, error) {
	cmd := g.command(ctx, dir, authed, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, gitError(ctx, err, stderr.String())
	}
	return out, nil
}

// start runs a git command and streams its stdout. The caller must close the
// reader and Wait the command.
func (g *gitSource) start(ctx context.Context, dir string, args ...string) (*exec.Cmd, io.ReadCloser, error) {
	cmd := g.command(ctx, dir, false, args...)
	cmd.Stderr = io.Discard
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, gitError(ctx, err, "")
	}
	return cmd, pipe, nil
}

// runCapped runs a network git command while watching how much disk it has
// pulled, killing it if the repo blows past the per-repo cap. This bounds a
// hostile or simply enormous repo before it fills the volume, instead of
// discovering the problem after the download. authed selects credentialed vs
// anonymous transport; callers start anonymous and retry authed (see
// retryAuthed).
func (g *gitSource) runCapped(ctx context.Context, authed bool, dir, watch string, args ...string) ([]byte, error) {
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()

	over := make(chan struct{})
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if dirSize(watch) > g.maxBytes {
					close(over)
					cancel()
					return
				}
			}
		}
	}()

	out, err := g.runEnv(cctx, authed, dir, args...)
	close(done)
	// The polling goroutine prevents sustained growth past the limit, but a
	// fast clone can finish between ticks. Check once more before installing
	// its temporary directory as a usable mirror.
	if dirSize(watch) > g.maxBytes {
		return nil, errRepoTooLarge
	}
	if err != nil {
		select {
		case <-over:
			return nil, errRepoTooLarge
		default:
		}
	}
	return out, err
}

// gitError maps git's failure modes onto the API's error vocabulary. A missing
// or private repo is reported the same way GitHub's REST API reports it, so an
// unauthenticated probe can't distinguish "private" from "does not exist".
func gitError(ctx context.Context, err error, stderr string) error {
	if ctx.Err() != nil {
		return errCloneTimeout
	}
	var ee *exec.Error
	if errors.As(err, &ee) {
		return errGitUnavailable
	}
	s := strings.ToLower(stderr)
	switch {
	case strings.Contains(s, "not found"),
		strings.Contains(s, "could not read username"),
		strings.Contains(s, "authentication failed"),
		strings.Contains(s, "access denied"),
		strings.Contains(s, "invalid username or password"):
		return errNotFound
	case strings.Contains(s, "rate limit"), strings.Contains(s, "too many requests"),
		// curl's bare HTTP-status phrasing ("...returned error: 429"), which
		// is what anonymous throttling often looks like from git.
		strings.Contains(s, "returned error: 429"):
		return errRateLimit
	}
	// stderr can carry repo content (branch names, subjects) but never the
	// token: it only ever travels in the environment.
	return &apiError{status: 502, code: "git_error",
		msg: "git failed: " + firstLine(stderr)}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	if s == "" {
		s = "unknown error"
	}
	return s
}

// ---- mirror cache ----

// mirrorPath is the on-disk mirror for a repo. The name stays readable for
// operators, and a short digest of the exact "owner/name" removes any chance
// that two different repos collide onto one directory.
func (g *gitSource) mirrorPath(repo string) (string, error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || !safePathPart(owner) || !safePathPart(name) {
		return "", &apiError{status: 400, code: "bad_repo", msg: "repo must be owner/name"}
	}
	sum := sha256.Sum256([]byte(repo))
	return filepath.Join(g.dir, owner+"__"+name+"-"+hex.EncodeToString(sum[:4])+".git"), nil
}

// safePathPart rejects the path components that would let a repo name escape
// the clone directory. repoRe already constrains the character set, but "." and
// ".." pass it, and this path is a filesystem write.
func safePathPart(s string) bool {
	return s != "" && s != "." && s != ".." &&
		!strings.ContainsAny(s, "/\\") && !strings.HasPrefix(s, "-")
}

// sweep bounds the mirror cache: it drops mirrors nothing has touched for
// `ttl`, then evicts least-recently-used mirrors until the cache fits its disk
// budget.
//
// Deletion is arbitrated by the mirror gate, not by timestamps: the sweep can
// only remove a directory it managed to mark as deleting while no build held
// it, so a fetch, a parse, or a build waiting for either is never evicted
// mid-use. The mtime rules below are only how a directory becomes a candidate.
func (g *gitSource) sweep() {
	if !g.sweeping.TryLock() {
		return // another pass is already running
	}
	defer g.sweeping.Unlock()

	entries, err := os.ReadDir(g.dir)
	if err != nil {
		return
	}
	type mirror struct {
		path string
		used time.Time
		size int64
	}
	var mirrors []mirror
	var total int64
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.Contains(name, ".git.tmp") && !strings.HasSuffix(name, ".git") {
			// Leftover clone dir from a process that was killed mid-clone.
			// The build that owned it cannot outlive CLONE_TIMEOUT (the
			// process group is killed at the deadline), so past that the
			// tmp dir is provably abandoned. A valid mirror always ends in
			// .git even when ".git.tmp" appears inside its repo name.
			if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > g.timeout {
				_ = os.RemoveAll(filepath.Join(g.dir, name))
			}
			continue
		}
		if !strings.HasSuffix(name, ".git") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(g.dir, name)
		size := dirSize(path)
		total += size
		idle := time.Since(info.ModTime())
		if idle < g.timeout {
			continue // possibly in use right now
		}
		if idle > g.ttl {
			if g.gate.beginDelete(path) {
				err := os.RemoveAll(path)
				g.gate.endDelete(path)
				if err == nil {
					total -= size
				}
			}
			continue
		}
		mirrors = append(mirrors, mirror{path, info.ModTime(), size})
	}
	if total <= g.diskCap {
		return
	}
	sort.Slice(mirrors, func(i, j int) bool { return mirrors[i].used.Before(mirrors[j].used) })
	for _, m := range mirrors {
		if total <= g.diskCap {
			return
		}
		// The mtime read above is as old as the dirSize walk. A build may
		// have leased the mirror since; the gate refuses the delete then.
		if !g.gate.beginDelete(m.path) {
			continue
		}
		err := os.RemoveAll(m.path)
		g.gate.endDelete(m.path)
		if err == nil {
			total -= m.size
			log.Printf("git: evicted mirror %s (%d MB)", filepath.Base(m.path), m.size>>20)
		}
	}
}

// mirrorGate arbitrates one mirror directory between its builds and the sweep.
//
// The mtime lease alone (touch before use, sweep skips dirs younger than
// CLONE_TIMEOUT) cannot close the eviction race: a sweep's Stat-then-RemoveAll
// is not atomic against a build that leases the mirror in between, and two
// builds sharing one mirror (same repo, different HEADs) can run a fetch and a
// refetch's RemoveAll against each other. Every build holds its mirror's gate
// from before ensureMirror until the parse is done, and the sweep may only
// delete a directory it can mark as deleting while no build holds it.
type mirrorGate struct {
	mu       sync.Mutex
	cond     *sync.Cond
	inUse    map[string]bool
	deleting map[string]bool
}

func newMirrorGate() *mirrorGate {
	g := &mirrorGate{inUse: map[string]bool{}, deleting: map[string]bool{}}
	g.cond = sync.NewCond(&g.mu)
	return g
}

// acquire blocks until dir is free and not being deleted.
func (g *mirrorGate) acquire(dir string) {
	g.mu.Lock()
	for g.inUse[dir] || g.deleting[dir] {
		g.cond.Wait()
	}
	g.inUse[dir] = true
	g.mu.Unlock()
}

func (g *mirrorGate) release(dir string) {
	g.mu.Lock()
	delete(g.inUse, dir)
	g.cond.Broadcast()
	g.mu.Unlock()
}

// beginDelete marks dir as being removed unless a build holds it, in which
// case it returns false and the caller must not delete. On true, the caller
// owns the removal and must call endDelete when done.
func (g *mirrorGate) beginDelete(dir string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.inUse[dir] || g.deleting[dir] {
		return false
	}
	g.deleting[dir] = true
	return true
}

func (g *mirrorGate) endDelete(dir string) {
	g.mu.Lock()
	delete(g.deleting, dir)
	g.cond.Broadcast()
	g.mu.Unlock()
}

func dirSize(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// ---- env helpers ----

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(strings.TrimSpace(getenv(key))); err == nil && v > 0 {
		return v
	}
	return def
}

func envDur(key string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(strings.TrimSpace(getenv(key))); err == nil && d > 0 {
		return d
	}
	return def
}

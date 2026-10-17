package main

import (
	"fmt"
	"math"
	"sort"
)

// paramsVersion invalidates cached blobs whenever the emitted contract shape
// changes. Bump the v2 prefix by hand on any transform change; the suffix
// tracks the sampling knobs automatically, so moving one of them can never
// silently serve contracts the old knobs shaped.
var paramsVersion = fmt.Sprintf("v2-s%d-b%d", maxStars, branchCap)

// Star-count ceiling for the current one-sprite-per-star renderer. Kept below
// the fps cliff for that architecture; measured empirically in-browser.
const maxStars = 1400

// branchCap is how many asterisms (merged PRs + live branches) the legend
// carries around the trunk.
const branchCap = 8

// Baselines read from the locked renderer constants so a repo near this size
// reproduces today's look.
const (
	s0     = 46.0
	span0  = 26.0
	turns0 = 2.15
	grow0  = 14.0 // baseline reveal duration (seconds)

	turnsMax = 3.2
	growMax  = 22.0
)

// ---- contract (the boundary emitted to the renderer) ----

type Contract struct {
	Repo        string       `json:"repo"`
	HeadSHA     string       `json:"headSha"`
	CommitTotal int          `json:"commitTotal"`
	Scale       Scale        `json:"scale"`
	Main        []MainNode   `json:"main"`
	Branches    []BranchNode `json:"branches"`
}

type Scale struct {
	Stars    int     `json:"stars"`
	Span     float64 `json:"span"`
	Turns    float64 `json:"turns"`
	Duration float64 `json:"duration"`
}

type MainNode struct {
	Type   string  `json:"type"` // genesis | release | commit
	Tier   int     `json:"tier"` // 1..4
	BirthT float64 `json:"birthT"`
	SHA    string  `json:"sha"`
	Label  string  `json:"label,omitempty"`
}

type BranchNode struct {
	N    string  `json:"n"`
	Sub  string  `json:"sub"`
	At   float64 `json:"at"`
	K    int     `json:"k"`
	Kind string  `json:"kind"` // pr | ref
}

// evenIndices returns `count` 1-based page numbers spread evenly across
// [1..total], ascending, always including 1 (HEAD) and total (genesis).
func evenIndices(total, count int) []int {
	if count >= total {
		out := make([]int, total)
		for i := range out {
			out[i] = i + 1
		}
		return out
	}
	seen := map[int]bool{}
	out := make([]int, 0, count)
	for i := 0; i < count; i++ {
		idx := 1 + int(math.Round(float64(i)*float64(total-1)/float64(count-1)))
		if idx < 1 {
			idx = 1
		}
		if idx > total {
			idx = total
		}
		if !seen[idx] {
			seen[idx] = true
			out = append(out, idx)
		}
	}
	sort.Ints(out)
	return out
}

// computeScale derives the galaxy geometry from the star count S.
// Geometry grows with sqrt(S/S0) so density stays constant.
func computeScale(stars int) Scale {
	s := float64(stars)
	f := math.Sqrt(s / s0)
	span := span0 * f
	turns := clamp(turns0*f, turns0, turnsMax)
	dur := clamp(grow0*math.Pow(s/s0, 0.25), grow0, growMax)
	return Scale{
		Stars:    stars,
		Span:     round2(span),
		Turns:    round2(turns),
		Duration: round2(dur),
	}
}

// buildMain turns the parsed commits (newest-first) into the ordered trunk
// (oldest-first), sampling to maxStars while force-including milestones.
func buildMain(commits []commitRec, tagSHA map[string]string) []MainNode {
	n := len(commits)
	if n == 0 {
		return nil
	}
	// milestone commit indices (release/tag targets present in this set).
	isMilestone := make([]bool, n)
	label := make([]string, n)
	for i, c := range commits {
		if lbl, ok := tagSHA[short(c.SHA)]; ok {
			isMilestone[i] = true
			label[i] = lbl
		}
	}

	var pick []int
	if n <= maxStars {
		pick = make([]int, n)
		for i := range pick {
			pick[i] = i
		}
	} else {
		pick = sampleWithMilestones(n, maxStars, isMilestone)
	}

	// date range for birthT normalization (genesis = oldest, HEAD = newest).
	headDate := commits[0].Committed
	genesisDate := commits[n-1].Committed
	span := headDate.Sub(genesisDate).Seconds()

	// emit oldest -> newest (reverse of newest-first order).
	sort.Sort(sort.Reverse(sort.IntSlice(pick)))
	out := make([]MainNode, 0, len(pick))
	for _, i := range pick {
		c := commits[i]
		bt := 0.0
		if span > 0 {
			bt = c.Committed.Sub(genesisDate).Seconds() / span
		}
		node := MainNode{
			Type:   "commit",
			Tier:   1,
			BirthT: round4(clampf(bt, 0, 1)),
			SHA:    short(c.SHA),
		}
		switch {
		case i == n-1:
			node.Type = "genesis"
			node.Tier = 4
			node.BirthT = 0
		case isMilestone[i]:
			node.Type = "release"
			node.Tier = 3
			node.Label = label[i]
		case len(c.Parents) > 1:
			node.Tier = 2 // merge commit
		}
		out = append(out, node)
	}
	// Git committer dates are not strictly ordered (rebases, merges), so raw
	// birthT can dip below its predecessor. Enforce non-decreasing birthT so the
	// reveal always grows outward from genesis, keeping real spacing where the
	// dates are already ordered. Genesis stays 0; HEAD pinned to exactly 1.
	for i := 1; i < len(out); i++ {
		if out[i].BirthT < out[i-1].BirthT {
			out[i].BirthT = out[i-1].BirthT
		}
	}
	// A single-commit repo has one node that is both genesis and HEAD: genesis
	// wins, it must ignite at 0.
	if len(out) > 1 {
		out[len(out)-1].BirthT = 1
	}
	return out
}

// sampleWithMilestones picks `count` indices from [0..n) evenly, then swaps in
// every milestone index by replacing the nearest non-milestone, non-endpoint
// pick so the final count stays exactly `count`.
func sampleWithMilestones(n, count int, isMilestone []bool) []int {
	chosen := make([]bool, n)
	base := evenIndices(n, count) // 1-based ascending
	for _, p := range base {
		chosen[p-1] = true
	}
	endpoints := map[int]bool{0: true, n - 1: true}

	for m := 0; m < n; m++ {
		if !isMilestone[m] || chosen[m] {
			continue
		}
		// find nearest chosen index that is safe to evict.
		best, bestDist := -1, 1<<62
		for _, p := range base {
			j := p - 1
			if !chosen[j] || endpoints[j] || isMilestone[j] {
				continue
			}
			if d := abs(j - m); d < bestDist {
				bestDist, best = d, j
			}
		}
		if best >= 0 {
			chosen[best] = false
			chosen[m] = true
		}
	}

	out := make([]int, 0, count)
	for i := 0; i < n; i++ {
		if chosen[i] {
			out = append(out, i)
		}
	}
	return out
}

// buildBranches assembles asterisms from merged PRs (primary) plus live
// non-default branches, deduped by ref name, capped, placed by birthT. Both
// inputs are already measured against the trunk by the git source, so this is
// pure placement: no network calls happen here any more.
func buildBranches(pulls []prMerge, branchList []branchRec,
	genesisDate, headDate float64) []BranchNode {

	span := headDate - genesisDate
	norm := func(unix float64) float64 {
		if span <= 0 {
			return 0.5
		}
		return round4(clampf((unix-genesisDate)/span, 0.03, 0.97))
	}

	seen := map[string]bool{}
	// Non-nil from the start: the contract promises BranchNode[], and a Git
	// repo with no PRs or live branches must still serialize "branches": []
	// rather than null (which the renderer's .forEach would crash on).
	out := []BranchNode{}

	// Merged PRs first, newest first, deduped by PR number when known (two
	// PRs can share a squash-merge title; by ref otherwise).
	for _, p := range pulls {
		if len(out) >= branchCap {
			break
		}
		key := prKey(p)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		// A squashed PR's own commits are gone from the history, so its size is
		// genuinely unknown: say so rather than print a made-up count.
		sub := fmt.Sprintf("PR #%d · %s", p.Number, plural(p.Commits, "commit"))
		k := clampInt(p.Commits, 2, 6)
		if p.Commits <= 0 {
			sub = fmt.Sprintf("PR #%d · squashed", p.Number)
			k = 3
		}
		out = append(out, BranchNode{
			N:    p.Ref,
			Sub:  sub,
			At:   norm(float64(p.MergedAt.Unix())),
			K:    k,
			Kind: "pr",
		})
	}

	// fill remaining slots with live non-default branches.
	for _, b := range branchList {
		if len(out) >= branchCap {
			break
		}
		if b.Name == "" || seen[b.Name] || b.AheadBy <= 0 {
			continue
		}
		seen[b.Name] = true
		out = append(out, BranchNode{
			N:    b.Name,
			Sub:  "branch · " + plural(b.AheadBy, "commit"),
			At:   norm(float64(b.BaseAt.Unix())),
			K:    clampInt(b.AheadBy, 2, 6),
			Kind: "ref",
		})
	}
	return out
}

// ---- small numeric helpers ----

func clamp(v, lo, hi float64) float64  { return math.Max(lo, math.Min(hi, v)) }
func clampf(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
func round2(v float64) float64 { return math.Round(v*100) / 100 }
func round4(v float64) float64 { return math.Round(v*10000) / 10000 }
func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

package main

import (
	"encoding/json"
	"testing"
	"time"
)

// The contract promises branches: BranchNode[] (packages/contract). A Go nil
// slice marshals as null, which crashes the renderer's BRANCH_DEFS.forEach -
// so a repo with no PRs and no live branches must still emit "branches": [].
func TestContractBranchesSerializesAsArray(t *testing.T) {
	b, err := json.Marshal(buildBranches(nil, nil, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "[]" {
		t.Errorf("empty branches marshaled as %s, want []", b)
	}
}

// A repo with a single commit has one node that is both genesis and HEAD. The
// contract promises genesis at birthT 0 (it ignites first, per the locked
// renderer), so the HEAD pin must not overwrite it.
func TestBuildMainSingleCommitGenesisStaysZero(t *testing.T) {
	c := commitRec{SHA: "0123456789abcdef", Committed: time.Unix(1700000000, 0).UTC()}
	out := buildMain([]commitRec{c}, nil)
	if len(out) != 1 {
		t.Fatalf("len(main) = %d, want 1", len(out))
	}
	if out[0].Type != "genesis" {
		t.Errorf("type = %q, want genesis", out[0].Type)
	}
	if out[0].BirthT != 0 {
		t.Errorf("genesis birthT = %v, want 0", out[0].BirthT)
	}
}

// On a repo with more than one commit, the newest node must be pinned to
// birthT 1 so the reveal ends exactly at HEAD. Equal timestamps are the case
// that matters: with a real date span HEAD normalizes to 1 on its own, but
// when every sampled commit shares one date (or HEAD predates an ancestor
// through a rebase) the pin is the only thing that ends the reveal at 1.
func TestBuildMainHeadPinnedToOne(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	commits := []commitRec{
		{SHA: "aaaaaaaaaaaaaaaa", Committed: base}, // HEAD
		{SHA: "bbbbbbbbbbbbbbbb", Committed: base},
		{SHA: "cccccccccccccccc", Committed: base}, // genesis
	}
	out := buildMain(commits, nil)
	if len(out) != 3 {
		t.Fatalf("len(main) = %d, want 3", len(out))
	}
	if out[0].Type != "genesis" || out[0].BirthT != 0 {
		t.Errorf("first = %+v, want genesis with birthT 0", out[0])
	}
	if out[len(out)-1].BirthT != 1 {
		t.Errorf("head birthT = %v, want 1", out[len(out)-1].BirthT)
	}
	for i := 1; i < len(out); i++ {
		if out[i].BirthT < out[i-1].BirthT {
			t.Errorf("birthT not monotonic at %d: %v < %v", i, out[i].BirthT, out[i-1].BirthT)
		}
	}
}

// Two PRs that share a squash-merge title are still two asterisms; dedupe must
// key on the PR number, not the title.
func TestBuildBranchesDedupesByPRNumberNotTitle(t *testing.T) {
	pulls := []prMerge{
		{Number: 1, Ref: "fix tests", MergedAt: time.Unix(50, 0).UTC()},
		{Number: 2, Ref: "fix tests", MergedAt: time.Unix(80, 0).UTC()},
	}
	out := buildBranches(pulls, nil, 0, 100)
	if len(out) != 2 {
		t.Fatalf("len(branches) = %d, want 2 (same-title PRs are distinct)", len(out))
	}
}

// Tags must be matched on the full commit SHA. In a large history two commits
// routinely share a 7-character prefix, and a prefix match would label an
// untagged commit as a release (and force it into the sampled trunk).
func TestBuildMainTagMatchesFullSHANotPrefix(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	tagged := "abcdef0111111111111111111111111111111111"
	commits := []commitRec{
		{SHA: tagged, Committed: base.Add(2 * time.Hour)},                                 // HEAD, tagged
		{SHA: "abcdef0222222222222222222222222222222222", Committed: base.Add(time.Hour)}, // same short SHA, untagged
		{SHA: "9999999999999999999999999999999999999999", Committed: base},                // genesis
	}
	out := buildMain(commits, map[string]string{tagged: "v1.0.0"})
	releases := 0
	for _, n := range out {
		if n.Type == "release" {
			releases++
			if n.Label != "v1.0.0" {
				t.Errorf("release label = %q", n.Label)
			}
		}
	}
	if releases != 1 {
		t.Fatalf("releases = %d, want exactly 1 (prefix collision must not tag a second commit)", releases)
	}
	if out[len(out)-1].Type != "release" {
		t.Errorf("tagged HEAD = %+v, want release", out[len(out)-1])
	}
}

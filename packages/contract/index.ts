// Shared TypeScript mirror of the Go backend's Contract JSON
// (apps/server/transform.go). The Go service is the source of truth; keep these
// field names in sync with its `json:"..."` tags. Emitted by GET /api/starmap.

export interface Scale {
  /** number of trunk stars actually drawn (== main.length) */
  stars: number;
  /** galaxy radius; grows with sqrt(stars) so density stays constant */
  span: number;
  /** spiral winding */
  turns: number;
  /** reveal duration in seconds */
  duration: number;
}

export type MainNodeType = "genesis" | "release" | "commit";

export interface MainNode {
  type: MainNodeType;
  /** magnitude tier 1..4 (genesis=4, release=3, merge=2, else 1) */
  tier: number;
  /** normalized birth time 0..1 across [genesis, HEAD] */
  birthT: number;
  /** 7-char commit sha */
  sha: string;
  /** release/tag name, present only on release nodes */
  label?: string;
}

export type BranchKind = "pr" | "ref";

export interface BranchNode {
  /** real branch or PR head-ref name */
  n: string;
  /** label subtitle, e.g. "PR #123 · 6 commits" or "branch · 4 commits" */
  sub: string;
  /** branch-off point normalized 0..1 */
  at: number;
  /** commit count on the branch/PR, clamped 2..6 (asterism size) */
  k: number;
  kind: BranchKind;
}

export interface Contract {
  repo: string;
  headSha: string;
  commitTotal: number;
  scale: Scale;
  main: MainNode[];
  branches: BranchNode[];
}

/** Error body shape for non-200 responses from /api/starmap. */
export interface ApiError {
  error: string;
  message?: string;
}

// A synthesized, deterministic `Contract` for the landing-page previews, so the
// real Three.js renderer (lib/starmap/renderer.ts) can run WITHOUT the Go
// backend (`/api/starmap`). Shapes match packages/contract exactly. It is a
// pure function of the repo string, so the same name always charts the same
// demo galaxy. The live product still fetches real history at /map.

import type { Contract, MainNode, BranchNode } from "@stellar/contract";
import { hashStr, rng } from "./seed";

const BRANCH_POOL: Array<{ n: string; kind: "pr" | "ref" }> = [
  { n: "feat/parser", kind: "ref" },
  { n: "fix/edge-cases", kind: "ref" },
  { n: "pr-142-streaming", kind: "pr" },
  { n: "refactor/core", kind: "ref" },
  { n: "pr-158-cache", kind: "pr" },
  { n: "docs/handbook", kind: "ref" },
  { n: "perf/render", kind: "ref" },
];

/** Build a plausible demo history for `repo` (default a nice medium galaxy). */
export function demoModel(repo = "sindresorhus/slugify"): Contract {
  const seed = hashStr(repo);
  const rand = rng(seed);
  const sha = () =>
    Array.from(
      { length: 7 },
      () => "0123456789abcdef"[Math.floor(rand() * 16)],
    ).join("");

  const N = 78 + Math.floor(rand() * 40); // trunk commit count
  const main: MainNode[] = [];
  // genesis
  main.push({ type: "genesis", tier: 4, birthT: 0, sha: sha() });

  // scatter a handful of releases through the trunk
  const relCount = 4 + Math.floor(rand() * 3);
  const relAt = new Set<number>();
  for (let i = 0; i < relCount; i++)
    relAt.add(Math.floor(0.25 * N + rand() * 0.7 * N));
  let major = 0,
    minor = 0;

  for (let i = 1; i < N; i++) {
    const birthT = i / (N - 1);
    if (relAt.has(i)) {
      if (rand() > 0.6) {
        major++;
        minor = 0;
      } else minor++;
      main.push({
        type: "release",
        tier: 3,
        birthT,
        sha: sha(),
        label: `v${major}.${minor}.0`,
      });
    } else {
      main.push({ type: "commit", tier: 1, birthT, sha: sha() });
    }
  }

  // branches / PRs departing the trunk at spread-out points
  const bCount = 5 + Math.floor(rand() * 2);
  const branches: BranchNode[] = [];
  const used = new Set<number>();
  for (let i = 0; i < bCount; i++) {
    let pick = Math.floor(rand() * BRANCH_POOL.length);
    while (used.has(pick) && used.size < BRANCH_POOL.length)
      pick = (pick + 1) % BRANCH_POOL.length;
    used.add(pick);
    const b = BRANCH_POOL[pick];
    const k = 2 + Math.floor(rand() * 5); // 2..6 asterism size
    const at = 0.28 + (i / bCount) * 0.56 + (rand() - 0.5) * 0.06;
    const sub =
      b.kind === "pr"
        ? `PR #${100 + Math.floor(rand() * 200)} · ${k} commits`
        : `branch · ${k} commits`;
    branches.push({ n: b.n, sub, at: Math.max(0.12, Math.min(0.9, at)), k, kind: b.kind });
  }

  return {
    repo,
    headSha: main[main.length - 1].sha,
    commitTotal: N + branches.reduce((s, b) => s + b.k, 0),
    scale: {
      stars: N,
      span: 22 + Math.round(rand() * 8), // medium galaxy
      turns: 2.0 + rand() * 0.8,
      duration: 6,
    },
    main,
    branches,
  };
}

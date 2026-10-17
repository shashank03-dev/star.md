# Stellar — Locked Design

**Status: LOCKED** as of 2026-07-31. Reference implementation:
`apps/web/lib/starmap/renderer.ts` (ported intact from the original
`starmap-3d.html`, which remains recoverable in git history).
Nothing below changes without an explicit decision to unlock it.

---

## The product

One website, three parts:

| Part | What it is | Phase |
|---|---|---|
| **A · Landing page** | Marketing front. Star effect lives in a **CTA-sized component**, not a full-bleed hero. | Phase 1 |
| **B · Star map** | Paste a repo URL → 3D nebula galaxy of its history. **This is locked.** | Phase 1 |
| **C · README artifact** | Embeddable, live-updating version | Phase 2 |

---

## The locked metaphor

A repository is a **galaxy that grows outward from its own first commit**.

- **The initial commit is the center.** Position `(0,0,0)`. Largest, warmest star, 6 diffraction spikes, its own amber core-nebula. It ignites **alone first** (~1.9s) before any history exists.
- **History grows outward from it** by timestamp (~14s). Main branch spirals out as a **warped 3D disc**, not a flat pinwheel. Camera pulls back as the galaxy grows.
- **Commits are stars.** **Branches are constellations.** **PRs are the lines home.** **Releases are milestones.**

## Locked visual rules

**Connections**
- **Solid** fat lines → main trunk
- **Dotted** lines → branch stems departing the trunk, and merge lines returning home
- **~1,500 flowing dots** ride every revealed edge, parent → child, brightest mid-segment, fading at both ends. Depth-correct point sizing.

**Nebula (gaseous, real 3D)**
- 16 volumetric cloud billboards from **domain-warped FBM** — this is what makes them wispy/filamentary instead of blobby. Non-negotiable.
- Varying depths (real parallax when orbiting), tinted indigo / violet / teal / ember, slow spin + drift
- Exponential fog + two starfield shells

**Asterism logic (the anti-AI core)**
- The constellation line **is the real commit chain**. Star `i` = commit `i`. We choose *where* true points sit, never invent points.
- **Archetype chosen by `hash(branch name)`** — hook / arc / kite / wzig / wave. Stops the sky looking like N photocopies.
- **Non-uniform spacing** along the skeleton. Uniform spacing is the #1 generated tell.
- **4 quantized magnitude tiers**, never a continuous size ramp. Only top tiers get diffraction spikes.
- **Bayer letters (α β γ…) encode real magnitude order**, not chronology. The merge commit is usually α.
- **Organic but stable per repo** — all randomness seeded from repo/branch names, so the same history always draws the same chart. Never re-scatters on reload.

**Rendering**
- **Three.js `0.185.1`** + EffectComposer / UnrealBloom / Line2 / CSS2DRenderer / OrbitControls
- **Hybrid split:** bloom touches **only stars**; all labels/chrome are DOM/CSS2D so type stays crisp
- Screen-space cartouche frame + corner ticks (does not fight the 3D camera)
- Drag to orbit, scroll to zoom

---

## Still open (deliberately)

- Landing page typography — **to be fully redefined**
- Landing background — "aesthetic white", **not** pure `#fff`
- Star effect placement/intensity inside the CTA — refine along the way
- Backend: Go. Frontend: React + react-three-fiber. Components from framer.ai / 21st.dev.

## Rejected directions (do not revisit)

- Gold/violet glow cosmic skin — "hated it"
- Linear-token flat minimalism — "not worthy enough"
- Flat 2D horizontal river chart — superseded by 3D radial growth
- Force-directed layout — reads as generic AI node-soup

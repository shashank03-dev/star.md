# star.md / Stellar (ported from Claude Code memory)

Turns a GitHub repo's history into an animated star map. Backend Go, frontend React +
react-three-fiber. Reference implementation: `starmap-3d.html`; authoritative spec:
`DESIGN-LOCK.md` (committed). Phase 2 is the live-updating README embed.

## Star-map visual design - LOCKED (2026-07-31)

Initial commit sits at (0,0,0) and ignites alone before history grows outward; main branch
is a warped 3D disc; branches are constellations whose archetype is chosen by
`hash(branch name)`; solid trunk lines + dotted stem/merge lines; ~1500 dots flowing along
edges; domain-warped FBM nebula billboards at varying depths. Three.js 0.185.1, bloom on
stars only, CSS2D labels for crisp type.

**Rejected - do not revisit:** gold/violet glow cosmic skin, Linear-token flat minimalism,
flat 2D horizontal river chart, force-directed layout.

**Still open:** landing-page typography, "aesthetic white" (not pure #fff) landing
background, star effect as a CTA-sized component not a full-bleed hero. Treat
`DESIGN-LOCK.md` as authoritative and never re-propose a rejected direction.

## Landing hero (apps/web, Next.js 16 static export, React 19)

3D-depth scene: realistic canvas sky (`StarSky`: Milky Way dust band, magnitude-distributed
black-body stars, rare spiked stars, meteors), floating star-map window with an RA/Dec
reticle, layered horizon from two alpha-cutout webps (`horizon-far.webp` behind,
`horizon-near.webp` occluding the lower edge). Flow: hero -> legend (pinned scrub) -> README
embed (`components/landing/Embed.tsx`, real badge preview + copy snippet) -> closing field.
Type: Mona Sans (wide, display), Geist (body), Geist Mono (labels). Key files:
`components/Landing.tsx`, `app/landing.css`, `components/Legend.tsx`,
`components/landing/RepoForm.tsx`, `lib/site.ts` (public URLs/examples), `lib/anim.ts` (lazy
Lenis+GSAP+ScrollTrigger loader, one shared ticker), `components/MapPreview.tsx`,
`scripts/horizon.py` (regenerates horizons from `horizon-source.jpg`; run from `apps/web`).

- The hero pin sits above the legend, so its ScrollTrigger needs `refreshPriority: 1`
  (HIGHER refreshes earlier) or legend triggers desync.
- User direction (2026-08-02): the landing uses the REAL Three.js renderer
  (`lib/starmap/renderer.ts`) - HERO autoplays the galaxy + slow auto-orbit, LEGEND pins the
  same renderer and SCRUBS its growth choreography by scroll. Data via synthesized demo
  `Contract` client-side (`lib/starmap/demoModel.ts`); no Go backend needed. Keep the 2D
  `MapPreview` only as the degraded tier (reduced-motion/weak/touch).

## Backend scaling direction (agreed, not yet built as of 2026-08-03)

Do NOT lean on a single personal GitHub token (`apps/server/github.go` `resolveToken` costs
~200-1400 REST calls per cold repo against one 5,000/hr budget). Get commit graph +
branches + tags from a **blob-less bare git clone** (`git clone --bare --filter=tree:0`) +
local `git log` (public repos clone anonymously, zero token). Use the token only for merged
PRs and private repos; `git ls-remote <url> HEAD` for staleness checks. Bounded bare-mirror
disk cache + incremental fetch (LRU/TTL), single-flight per repo, bounded concurrency +
timeouts + max-repo-size cap, harden user-URL cloning (pin github.com, GIT_TERMINAL_PROMPT=0,
no hooks/submodules, never log token URLs). **Hard constraint:** the clone-parse path must
emit the identical `Contract` (packages/contract) so the locked galaxy look is unchanged.
Deployment stays single-container per README unless told otherwise.

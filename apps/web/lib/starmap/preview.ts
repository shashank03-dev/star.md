// A lightweight 2D-canvas evocation of the real 3D star map (lib/starmap/
// renderer.ts, the WebGL renderer at /map). It is NOT a port — the landing page
// must stay light (no Three.js) — but it deliberately mirrors the real look:
// a warm golden genesis core, a multi-arm spiral of magnitude-tiered stars with
// diffraction spikes, wispy coloured nebulae, and trunk / branch / dotted-merge
// lines with light flowing along them.
//
// One model, two consumers:
//   • the hero window (MapPreview) renders it whole and alive (reveal = 1),
//   • the legend renders it BUILDING as you scroll, by advancing `Reveal`
//     stage by stage (genesis → commits → constellations → merges → releases).
//
// The galaxy is a pure function of a seed string, so the same repo always draws
// the same sky. All geometry lives in normalised units (radius ~1, centred at
// origin); a view maps units → pixels so it scales to any canvas.

import { hashStr, rng } from "./seed";

type RGB = [number, number, number];

export interface PStar {
  x: number;
  y: number;
  tier: number; // 0..4 magnitude
  col: RGB;
  warm: boolean;
  tw: number; // twinkle phase
  tws: number; // twinkle speed
  birth: number; // 0..1 reveal order along the trunk
  release: boolean; // ringed milestone
}

export interface PBranch {
  nodes: PStar[]; // the asterism's stars (tip last)
  poly: Array<{ x: number; y: number }>; // constellation stroke through them
  base: { x: number; y: number }; // where it departs the trunk
  merge: { x: number; y: number }; // trunk point it merges home to
  order: number; // 0..1 reveal order among branches
}

export interface Nebula {
  x: number;
  y: number;
  r: number;
  col: RGB;
  op: number;
}

export interface TrunkEdge {
  ax: number;
  ay: number;
  bx: number;
  by: number;
  birth: number;
}

export interface Galaxy {
  genesis: PStar;
  stars: PStar[]; // trunk commits (excludes genesis)
  branches: PBranch[];
  nebula: Nebula[];
  releases: PStar[];
  trunk: TrunkEdge[]; // spiral-arm chain (parent → child WITHIN an arm)
}

export interface Reveal {
  genesis: number; // 0..1
  commits: number;
  constellations: number;
  merges: number;
  releases: number;
}

export const FULL: Reveal = {
  genesis: 1,
  commits: 1,
  constellations: 1,
  merges: 1,
  releases: 1,
};

const STAR_COLS: RGB[] = [
  [255, 255, 255],
  [201, 220, 255],
  [178, 201, 255],
  [228, 240, 255],
];
const WARM: RGB = [255, 238, 205];
const NEB_TINTS: RGB[] = [
  [92, 126, 220],
  [140, 96, 214],
  [64, 168, 190],
  [96, 110, 196],
  [116, 84, 180],
];

// asterism skeletons (unit-ish), same spirit as the real renderer's ARCH set
const ARCH: number[][][] = [
  [[0, 0], [0.9, 0.5], [1.9, 0.7], [2.7, 0.3], [2.9, -0.4]],
  [[0, 0], [1.0, 0.55], [2.1, 0.75], [3.1, 0.4], [3.7, -0.2]],
  [[0, 0], [0.8, 0.7], [1.8, 1.05], [2.8, 0.45], [2.1, -0.45]],
  [[0, 0], [1.0, 0.65], [1.8, -0.2], [2.8, 0.6], [3.6, -0.15]],
  [[0, 0], [1.1, 0.4], [2.1, -0.25], [3.0, 0.45], [3.8, 0.05]],
];

/** Build the deterministic galaxy for a seed string (repo name, or anything). */
export function buildGalaxy(seedStr: string): Galaxy {
  const SEED = hashStr(seedStr);
  const rand = rng(SEED);
  const g = rng((SEED ^ 0x9e3779b9) >>> 0); // shape-only stream

  const ARMS = 2 + Math.floor(g() * 2); // 2..3 arms reads best small
  const CHIR = g() < 0.5 ? 1 : -1;
  const RADPOW = 0.86 + g() * 0.28;
  const TURNS = 0.82 + g() * 0.5;
  const ELLIP = 0.74 + g() * 0.16;
  const PHASE = g() * Math.PI * 2;
  const SPAN = 1;
  const N = 82; // trunk commits

  const genesis: PStar = {
    x: 0,
    y: 0,
    tier: 4,
    col: WARM,
    warm: true,
    tw: 0,
    tws: 1.1,
    birth: 0,
    release: false,
  };

  // commit[0] = genesis, commit[i] = trunk star i; trunk chains WITHIN an arm
  // (parent = i-ARMS, hub = genesis) so the lines trace clean spiral arcs
  // instead of crisscrossing between arms.
  const commit: PStar[] = [genesis];
  const stars: PStar[] = [];
  const trunk: TrunkEdge[] = [];
  for (let i = 1; i < N; i++) {
    const t = i / (N - 1);
    const arm = i % ARMS;
    const rr = Math.pow(t, RADPOW) * SPAN;
    const th =
      PHASE + arm * ((2 * Math.PI) / ARMS) + CHIR * t * TURNS * Math.PI * 2;
    const jitter = (rand() - 0.5) * 0.05;
    const x = Math.cos(th) * rr + jitter;
    const y = (Math.sin(th) * rr + jitter) * ELLIP;
    // magnitude: mostly faint, a few bright — brighter ones rarer
    const r = rand();
    const tier = r > 0.94 ? 3 : r > 0.78 ? 2 : r > 0.4 ? 1 : 0;
    const s: PStar = {
      x,
      y,
      tier,
      col: STAR_COLS[(rand() * STAR_COLS.length) | 0],
      warm: false,
      tw: rand() * 6.28,
      tws: 0.5 + rand() * 1.2,
      birth: t,
      release: false,
    };
    commit[i] = s;
    stars.push(s);
    const parent = commit[i >= ARMS ? i - ARMS : 0];
    trunk.push({ ax: parent.x, ay: parent.y, bx: x, by: y, birth: t });
  }

  // releases: a few of the brighter, outer commits, ringed + warmed
  const releases: PStar[] = [];
  const relCandidates = stars
    .filter((s) => s.birth > 0.28 && s.tier >= 2)
    .sort((a, b) => b.birth - a.birth);
  const RELN = Math.min(4, relCandidates.length);
  for (let i = 0; i < RELN; i++) {
    const s = relCandidates[(i * relCandidates.length) / RELN | 0];
    if (!s || s.release) continue;
    s.release = true;
    s.tier = Math.max(3, s.tier);
    s.warm = true;
    s.col = WARM;
    releases.push(s);
  }

  // branches: asterisms departing the trunk, each with a dotted merge home
  const branches: PBranch[] = [];
  const M = 5;
  for (let bi = 0; bi < M; bi++) {
    const bseed = hashStr(seedStr + "#b" + bi);
    const rb = rng(bseed);
    const at = 0.3 + (bi / M) * 0.5 + (rb() - 0.5) * 0.06; // trunk t it departs
    let base = genesis as PStar;
    for (const s of stars) if (s.birth <= at) base = s;
    const skel = ARCH[bseed % ARCH.length];

    // outward direction from centre, kicked tangentially, alternating side
    const bl = Math.hypot(base.x, base.y) || 1;
    const ox = base.x / bl,
      oy = base.y / bl;
    const side = bi % 2 ? 1 : -1;
    const ang = Math.atan2(oy, ox) + side * (0.5 + rb() * 0.5);
    const dx = Math.cos(ang),
      dy = Math.sin(ang);
    const px = -dy,
      py = dx; // perpendicular
    const SC = 0.12 + rb() * 0.05;

    const nodes: PStar[] = [];
    const poly: Array<{ x: number; y: number }> = [];
    for (let j = 0; j < skel.length; j++) {
      const s = skel[j];
      const x = base.x + (dx * s[0] + px * s[1] * side) * SC;
      const y = base.y + (dy * s[0] + py * s[1] * side) * SC;
      poly.push({ x, y });
      const isTip = j === skel.length - 1;
      nodes.push({
        x,
        y,
        tier: isTip ? 3 : j % 2 === 0 ? 2 : 1,
        col: STAR_COLS[(rb() * STAR_COLS.length) | 0],
        warm: false,
        tw: rb() * 6.28,
        tws: 0.5 + rb() * 1.2,
        birth: at,
        release: false,
      });
    }
    // merge home: nearest trunk star born after the branch, else genesis
    let merge = { x: genesis.x, y: genesis.y };
    for (const s of stars)
      if (s.birth >= at) {
        merge = { x: s.x, y: s.y };
        break;
      }
    branches.push({
      nodes,
      poly,
      base: { x: base.x, y: base.y },
      merge,
      order: bi / (M - 1),
    });
  }

  // nebulae: a few soft coloured clouds, offset from centre
  const nebula: Nebula[] = [];
  for (let i = 0; i < 6; i++) {
    const a = rand() * Math.PI * 2;
    const rr = 0.28 + rand() * 0.72;
    nebula.push({
      x: Math.cos(a) * rr,
      y: Math.sin(a) * rr * ELLIP,
      r: 0.36 + rand() * 0.4,
      col: NEB_TINTS[(SEED + i * 7) % NEB_TINTS.length],
      op: 0.1 + rand() * 0.12,
    });
  }

  return { genesis, stars, branches, nebula, releases, trunk };
}

export interface View {
  w: number;
  h: number;
  cx: number;
  cy: number;
  scale: number; // px per unit radius
  rot: number; // slow spin
  bg: number; // 0..1 opacity of the deep-space plate
}

const smooth = (t: number) => {
  const x = Math.max(0, Math.min(1, t));
  return x * x * (3 - 2 * x);
};
// per-item reveal: item at position `p` (0..1) fades in as the stage passes it
const at = (stage: number, p: number, feather = 0.12) =>
  smooth((stage - p) / feather);

/** Draw the galaxy into `ctx` at reveal `R` and animation time `t` (seconds). */
export function drawGalaxy(
  ctx: CanvasRenderingContext2D,
  gx: Galaxy,
  view: View,
  R: Reveal,
  t: number,
): void {
  const { w, h, cx, cy, scale, rot, bg } = view;
  const cos = Math.cos(rot),
    sin = Math.sin(rot);
  const P = (x: number, y: number): [number, number] => [
    cx + (x * cos - y * sin) * scale,
    cy + (x * sin + y * cos) * scale * 0.9,
  ];

  // deep-space plate (radial so canvas edges blend into the page)
  ctx.clearRect(0, 0, w, h);
  if (bg > 0) {
    const plate = ctx.createRadialGradient(cx, cy, 0, cx, cy, Math.max(w, h) * 0.62);
    plate.addColorStop(0, `rgba(10,15,30,${0.98 * bg})`);
    plate.addColorStop(0.55, `rgba(5,7,15,${0.96 * bg})`);
    plate.addColorStop(1, `rgba(3,4,10,${0.0 * bg})`);
    ctx.fillStyle = plate;
    ctx.fillRect(0, 0, w, h);
  }

  ctx.globalCompositeOperation = "lighter";

  // nebulae breathe in with the galaxy
  const nebA = 0.35 + 0.65 * smooth(R.commits * 1.1);
  for (const n of gx.nebula) {
    const [nx, ny] = P(n.x, n.y);
    const rad = n.r * scale;
    const drift = 1 + 0.04 * Math.sin(t * 0.25 + n.x * 4);
    const grd = ctx.createRadialGradient(nx, ny, 0, nx, ny, rad * drift);
    const [r, g, b] = n.col;
    grd.addColorStop(0, `rgba(${r},${g},${b},${n.op * nebA})`);
    grd.addColorStop(0.5, `rgba(${r},${g},${b},${n.op * nebA * 0.35})`);
    grd.addColorStop(1, `rgba(${r},${g},${b},0)`);
    ctx.fillStyle = grd;
    ctx.beginPath();
    ctx.arc(nx, ny, rad * drift, 0, 6.2832);
    ctx.fill();
  }

  // warm core glow behind genesis
  const gEase = smooth(R.genesis);
  const [gx0, gy0] = P(0, 0);
  const corePulse = 0.85 + 0.15 * Math.sin(t * 0.7);
  const coreR = scale * 0.5 * (0.7 + 0.3 * gEase);
  const core = ctx.createRadialGradient(gx0, gy0, 0, gx0, gy0, coreR);
  core.addColorStop(0, `rgba(255,210,150,${0.5 * gEase * corePulse})`);
  core.addColorStop(0.4, `rgba(255,190,120,${0.16 * gEase})`);
  core.addColorStop(1, "rgba(255,170,90,0)");
  ctx.fillStyle = core;
  ctx.beginPath();
  ctx.arc(gx0, gy0, coreR, 0, 6.2832);
  ctx.fill();

  // faint trunk lines tracing the spiral arms (reveal with commits)
  ctx.lineWidth = Math.max(1, scale * 0.005);
  for (const e of gx.trunk) {
    const f = at(R.commits, e.birth);
    if (f <= 0.02) continue;
    const [ax, ay] = P(e.ax, e.ay);
    const [bx, by] = P(e.bx, e.by);
    ctx.strokeStyle = `rgba(150,185,240,${0.16 * f})`;
    ctx.beginPath();
    ctx.moveTo(ax, ay);
    ctx.lineTo(bx, by);
    ctx.stroke();
  }

  // constellation strokes (draw progressively as each branch reveals)
  ctx.lineWidth = Math.max(1, scale * 0.006);
  for (const br of gx.branches) {
    const f = at(R.constellations, br.order, 0.2);
    if (f <= 0.02) continue;
    ctx.strokeStyle = `rgba(170,200,255,${0.5 * f})`;
    ctx.beginPath();
    const segs = br.poly.length - 1;
    const drawn = f * segs;
    for (let i = 0; i <= segs; i++) {
      const p = br.poly[i];
      const [x, y] = P(p.x, p.y);
      if (i === 0) ctx.moveTo(x, y);
      else if (i <= drawn) ctx.lineTo(x, y);
      else {
        // partial last segment
        const prev = br.poly[i - 1];
        const frac = drawn - (i - 1);
        const [px2, py2] = P(
          prev.x + (p.x - prev.x) * frac,
          prev.y + (p.y - prev.y) * frac,
        );
        ctx.lineTo(px2, py2);
        break;
      }
    }
    ctx.stroke();
  }

  // dotted merge lines home + light flowing along them
  for (const br of gx.branches) {
    const f = at(R.merges, br.order, 0.2);
    if (f <= 0.02) continue;
    const tip = br.nodes[br.nodes.length - 1];
    const [ax, ay] = P(tip.x, tip.y);
    const [bx, by] = P(br.merge.x, br.merge.y);
    ctx.setLineDash([2, 6]);
    ctx.lineWidth = Math.max(1, scale * 0.004);
    ctx.strokeStyle = `rgba(116,166,224,${0.5 * f})`;
    ctx.beginPath();
    ctx.moveTo(ax, ay);
    ctx.lineTo(ax + (bx - ax) * f, ay + (by - ay) * f);
    ctx.stroke();
    ctx.setLineDash([]);
    // flowing dots
    for (let k = 0; k < 3; k++) {
      const ft = ((t * 0.35 + k / 3) % 1) * f;
      const fx = ax + (bx - ax) * ft,
        fy = ay + (by - ay) * ft;
      const a = 0.5 * f * Math.sin(ft * Math.PI);
      ctx.fillStyle = `rgba(205,225,255,${a})`;
      ctx.beginPath();
      ctx.arc(fx, fy, Math.max(1, scale * 0.006), 0, 6.2832);
      ctx.fill();
    }
  }

  // stars
  const drawStar = (s: PStar, alpha: number) => {
    if (alpha <= 0.01) return;
    const [x, y] = P(s.x, s.y);
    const tw = 0.82 + 0.18 * Math.sin(t * s.tws * 2 + s.tw);
    const a = alpha * tw;
    const coreR = (0.006 + 0.004 * s.tier) * scale;
    const haloR = (0.02 + 0.03 * s.tier) * scale;
    const [r, g, b] = s.col;
    // halo
    const halo = ctx.createRadialGradient(x, y, 0, x, y, haloR);
    halo.addColorStop(0, `rgba(${r},${g},${b},${0.8 * a})`);
    halo.addColorStop(0.25, `rgba(${r},${g},${b},${0.25 * a})`);
    halo.addColorStop(1, `rgba(${r},${g},${b},0)`);
    ctx.fillStyle = halo;
    ctx.beginPath();
    ctx.arc(x, y, haloR, 0, 6.2832);
    ctx.fill();
    // diffraction spikes for the bright ones
    if (s.tier >= 2) {
      const len = haloR * (s.tier >= 4 ? 2.4 : s.tier === 3 ? 1.9 : 1.4);
      const spikes = s.tier >= 4 ? 6 : 4;
      ctx.lineWidth = s.tier >= 4 ? 2 : 1.2;
      ctx.lineCap = "round";
      for (let k = 0; k < spikes; k++) {
        const ang = (k * Math.PI * 2) / spikes + (s.tier === 3 ? Math.PI / 4 : 0);
        const ex = x + Math.cos(ang) * len,
          ey = y + Math.sin(ang) * len;
        const gr = ctx.createLinearGradient(x, y, ex, ey);
        gr.addColorStop(0, `rgba(${s.warm ? "255,246,225" : "245,250,255"},${0.9 * a})`);
        gr.addColorStop(1, "rgba(200,220,255,0)");
        ctx.strokeStyle = gr;
        ctx.beginPath();
        ctx.moveTo(x, y);
        ctx.lineTo(ex, ey);
        ctx.stroke();
      }
    }
    // core
    ctx.fillStyle = `rgba(255,255,255,${Math.min(1, a * 1.1)})`;
    ctx.beginPath();
    ctx.arc(x, y, coreR, 0, 6.2832);
    ctx.fill();
    // release ring
    if (s.release) {
      const ringA = smooth(R.releases) * a;
      if (ringA > 0.01) {
        ctx.lineWidth = Math.max(1, scale * 0.004);
        ctx.strokeStyle = `rgba(120,160,255,${0.55 * ringA})`;
        ctx.beginPath();
        ctx.arc(x, y, haloR * 1.5, 0, 6.2832);
        ctx.stroke();
      }
    }
  };

  for (const s of gx.stars) drawStar(s, at(R.commits, s.birth));
  for (const br of gx.branches) {
    const f = at(R.constellations, br.order, 0.2);
    for (const n of br.nodes) drawStar(n, f);
  }
  // genesis last, on top, pulsing
  const gPulse = 1 + 0.08 * Math.sin(t * 1.1);
  drawStar({ ...gx.genesis, tier: 4 }, gEase * gPulse);

  ctx.globalCompositeOperation = "source-over";
}

// The LOCKED star-map renderer, ported intact from the original starmap-3d.html
// module. The ONLY changes from that reference are mechanical:
//   1. imports: importmap CDN -> npm `three` + `three/examples/jsm/*`
//   2. entry: the top-level `await` is wrapped in initStarMap()
//   3. DOM: lookups are scoped to the passed `wrap` element, not `document`
//   4. teardown: a returned cleanup cancels the RAF loop, removes listeners, and
//      disposes GPU resources so route navigations / StrictMode remounts leak nothing
// No geometry, seeded-RNG, nebula, asterism, bloom, or choreography math changed.
// A small repo reproduces the locked reference look.

import * as THREE from "three";
import { EffectComposer } from "three/examples/jsm/postprocessing/EffectComposer.js";
import { RenderPass } from "three/examples/jsm/postprocessing/RenderPass.js";
import { UnrealBloomPass } from "three/examples/jsm/postprocessing/UnrealBloomPass.js";
import { OutputPass } from "three/examples/jsm/postprocessing/OutputPass.js";
import { Line2 } from "three/examples/jsm/lines/Line2.js";
import { LineGeometry } from "three/examples/jsm/lines/LineGeometry.js";
import { LineMaterial } from "three/examples/jsm/lines/LineMaterial.js";
import {
  CSS2DRenderer,
  CSS2DObject,
} from "three/examples/jsm/renderers/CSS2DRenderer.js";
import { OrbitControls } from "three/examples/jsm/controls/OrbitControls.js";
import type { Contract, ApiError } from "@stellar/contract";
import { hashStr, rng } from "./seed";

// Same-origin in production (Go serves both). Override for split dev via
// NEXT_PUBLIC_API_BASE (e.g. http://localhost:8770 when running next dev).
const API_BASE = process.env.NEXT_PUBLIC_API_BASE ?? "";

interface StarNode {
  id: number;
  type: string;
  branch: string;
  birthT: number;
  pos: THREE.Vector3;
  sha: string;
  label?: string;
  tier: number;
  tw: number;
  tws: number;
  bayer?: string;
}

/** Live control surface handed back via `options.onReady` for embedded previews. */
export interface StarMapPreviewApi {
  /** Scrub the growth choreography to a 0..1 position (genesis → present). */
  seek: (progress01: number) => void;
  /** Resume / pause the internal clock (autoplay). */
  play: () => void;
  pause: () => void;
}

export interface StarMapOptions {
  /** Inject the history model directly and skip the /api/starmap fetch. */
  model?: Contract;
  /** false → no user orbit/zoom/pan; a slow auto-orbit runs instead. */
  interactive?: boolean;
  /** Size the canvas to `wrap` (via ResizeObserver) instead of the window. */
  sizeToContainer?: boolean;
  /** false → the clock is externally scrubbed (see onReady.seek); default true. */
  autoplay?: boolean;
  /** false → skip the CSS2D labels (genesis / branch / bayer). Default true. */
  labels?: boolean;
  /** false → skip the scrub/replay HUD + corner ticks. Default true. */
  hud?: boolean;
  /** auto-orbit speed when non-interactive (OrbitControls units). Default 0.5. */
  autoRotate?: number;
  /** Called once the scene is live, with a control surface for previews. */
  onReady?: (api: StarMapPreviewApi) => void;
}

/**
 * Mount the star map into `wrap` (which must already contain the #labels,
 * #frame, #plate, #hint, #hud and #overlay markup) and load `repo`'s history.
 * Returns a teardown to call on unmount.
 *
 * `options` turns it into an embeddable preview (landing hero + legend): inject
 * a `model` to skip the backend, size to the container, drop interactivity/HUD,
 * and drive its growth from scroll via `onReady(api).seek`. With no options it
 * behaves exactly as the standalone /map route always did.
 */
export function initStarMap(
  wrap: HTMLElement,
  repo: string,
  options: StarMapOptions = {},
): () => void {
  let destroyed = false;
  let rafId = 0;
  let onResize: (() => void) | null = null;
  const gpuDisposers: Array<() => void> = [];
  let glCanvas: HTMLCanvasElement | null = null;

  const interactive = options.interactive !== false;
  const wantLabels = options.labels !== false;
  const wantHud = options.hud !== false;
  const sizeToC = !!options.sizeToContainer;
  const W = () => Math.max(1, sizeToC ? wrap.clientWidth : innerWidth);
  const H = () => Math.max(1, sizeToC ? wrap.clientHeight : innerHeight);

  const $ = <T extends HTMLElement = HTMLElement>(sel: string): T | null =>
    wrap.querySelector(sel) as T | null;

  const overlay = $("#overlay");
  const ovGlyph = $("#ovGlyph");
  const ovSub = $("#ovSub");

  async function loadModel(): Promise<Contract> {
    if (options.model) {
      overlay?.classList.add("hidden");
      return options.model;
    }
    if (!repo) {
      overlay?.classList.add("error");
      if (ovGlyph) ovGlyph.textContent = "No repository";
      if (ovSub) ovSub.textContent = "add ?repo=owner/name to the URL";
      throw new Error("no repo");
    }
    if (ovSub) ovSub.textContent = "reading " + repo;
    try {
      const res = await fetch(
        API_BASE + "/api/starmap?repo=" + encodeURIComponent(repo),
      );
      if (!res.ok) {
        let code: string | number = res.status;
        try {
          code = ((await res.json()) as ApiError).error || code;
        } catch {}
        throw new Error(String(code));
      }
      const model = (await res.json()) as Contract;
      overlay?.classList.add("hidden");
      return model;
    } catch (err) {
      overlay?.classList.add("error");
      if (ovGlyph) ovGlyph.textContent = "Could not chart " + repo;
      if (ovSub) ovSub.textContent = String((err as Error).message || err);
      throw err;
    }
  }

  (async () => {
    let model: Contract;
    try {
      model = await loadModel();
    } catch {
      return; // overlay already reflects the error state
    }
    if (destroyed) return;

    const REPO = model.repo;
    const SEED = hashStr(REPO); // every seed below derives from this, so the
    const rand = rng(SEED); // whole galaxy is a pure function of the repo
    const scale = model.scale; // { stars, span, turns, duration }
    const SPAN0 = 26; // locked baseline span
    const SCALE = scale.span / SPAN0; // galaxy grows with repo size

    /* ─────────── build a repo history in 3D ─────────── */
    const GREEK = ["α", "β", "γ", "δ", "ε", "ζ", "η", "θ", "ι", "κ"];
    const BRANCH_DEFS = model.branches; // real refs/PRs: { n, sub, at, k, kind }

    const nodes: StarNode[] = [];
    const edges: Array<{ a: number; b: number; kind: string }> = [];
    let nid = 0;
    const mk = (o: Omit<StarNode, "id">): StarNode => {
      const n = o as StarNode;
      n.id = nid++;
      nodes.push(n);
      return n;
    };
    const sha = () =>
      Array.from(
        { length: 7 },
        () => "0123456789abcdef"[Math.floor(rand() * 16)],
      ).join("");

    /* MAIN — a 3D spiral growing outward from the ORIGIN (the initial commit).
       Geometry (position) uses even index spacing so the locked look is preserved;
       the real per-commit data (type/tier/sha/birthT) rides on top of it. SPAN/TURNS
       scale with repo size; WARP scales with SPAN so the disc stays proportional. */
    const MD = model.main;
    const MAIN = MD.length,
      SPAN = scale.span,
      WARP = 4.2 * SCALE;

    /* ── galaxy FORM — a deterministic per-repo structure ──
       The trunk is no longer one fixed swirl. Arm count, swirl direction, winding,
       radial curve, ellipticity, disc tilt and warp are all drawn from the repo
       seed, so every repo grows a genuinely different galaxy while the same repo is
       always identical. ARMS=1 collapses back to the original single spiral. */
    const grng = rng((SEED ^ 0x9e3779b9) >>> 0); // separate stream: shape only
    const ARMS = 1 + Math.floor(grng() * 4); // 1..4 spiral arms
    const CHIR = grng() < 0.5 ? 1 : -1; // swirl handedness
    const RADPOW = 0.85 + grng() * 0.5; // radial growth curve (core density)
    const TURNS = scale.turns * (0.7 + grng() * 0.7); // winding, still size-aware
    const ELLIP = 0.68 + grng() * 0.3; // disc ellipticity
    const TILT = (grng() - 0.5) * 0.9; // disc tilt (radians)
    const WARPA = 0.6 + grng() * 1.3; // warp amplitude
    const WARPF = 0.6 + grng() * 1.0; // warp frequency
    const PHASE = grng() * Math.PI * 2; // base angle offset
    const cT = Math.cos(TILT),
      sT = Math.sin(TILT);

    const main: StarNode[] = [];
    for (let i = 0; i < MAIN; i++) {
      const d = MD[i];
      const t = i / (MAIN - 1); // 0..1 outward (birth order)
      const arm = i % ARMS; // round-robin across the arms
      const rr = Math.pow(t, RADPOW) * SPAN; // t=0 → dead centre
      const th = PHASE + arm * ((2 * Math.PI) / ARMS) + CHIR * t * TURNS * Math.PI * 2;
      const x = Math.cos(th) * rr;
      const y0 = Math.sin(th) * rr * ELLIP;
      const z0 =
        Math.sin(th * WARPF + 1.1) * WARP * t * WARPA + (rand() - 0.5) * 1.1 * t * SCALE;
      const p = new THREE.Vector3(x, y0 * cT - z0 * sT, y0 * sT + z0 * cT); // tilt the disc plane
      const n = mk({
        type: d.type,
        branch: "main",
        birthT: d.birthT,
        pos: p,
        sha: d.sha,
        label: d.label,
        tier: d.tier,
        tw: rand() * 6.28,
        tws: 0.5 + rand() * 1.2,
      });
      main.push(n);
      if (i > 0) {
        const parent = i >= ARMS ? i - ARMS : 0; // chain within each arm; hub = genesis
        edges.push({ a: main[parent].id, b: n.id, kind: "trunk" });
      }
    }
    main[0].pos.set(0, 0, 0);

    /* BRANCHES — depart into genuine 3D directions, each an asterism */
    const ARCH: Record<string, number[][]> = {
      hook: [[0, 0, 0], [1.0, 0.55, 0.2], [2.1, 0.75, -0.15], [3.0, 0.35, 0.35], [3.2, -0.45, 0.1]],
      arc: [[0, 0, 0], [1.1, 0.6, -0.25], [2.3, 0.8, 0.2], [3.4, 0.45, 0.4], [4.1, -0.2, -0.1]],
      kite: [[0, 0, 0], [0.9, 0.8, 0.3], [2.0, 1.2, -0.2], [3.1, 0.5, 0.25], [2.3, -0.5, 0.4]],
      wzig: [[0, 0, 0], [1.1, 0.75, 0.2], [2.0, -0.2, -0.3], [3.1, 0.7, 0.25], [4.0, -0.15, 0.1]],
      wave: [[0, 0, 0], [1.2, 0.45, -0.3], [2.3, -0.3, 0.25], [3.3, 0.5, -0.2], [4.2, 0.05, 0.3]],
    };
    const AK = Object.keys(ARCH);
    interface Branch {
      n: string;
      sub: string;
      at: number;
      k: number;
      kind: string;
      archKey: string;
      nodes: StarNode[];
      base: StarNode;
      alpha: StarNode;
    }
    const branches: Branch[] = [];

    BRANCH_DEFS.forEach((bd, bi) => {
      const seedB = hashStr(bd.n);
      const rb = rng(seedB);
      const archKey = AK[seedB % AK.length];
      const skel = ARCH[archKey];

      let base = main[0];
      for (const m of main) if (m.birthT <= bd.at) base = m;

      /* orientation: outward from centre + tangential kick + 3D tilt */
      const outv = base.pos.clone().normalize();
      if (outv.lengthSq() < 0.01) outv.set(1, 0, 0);
      const up = new THREE.Vector3(0, 0, 1);
      const tang = new THREE.Vector3().crossVectors(up, outv).normalize();
      const side = bi % 2 ? 1 : -1;
      const tilt = (rb() - 0.5) * 1.5;

      const dir = outv
        .clone()
        .multiplyScalar(0.85)
        .add(tang.multiplyScalar(side * 0.75))
        .add(up.clone().multiplyScalar(tilt * 0.55))
        .normalize();
      const perp = new THREE.Vector3().crossVectors(dir, up).normalize();
      const perp2 = new THREE.Vector3().crossVectors(dir, perp).normalize();

      const SC = (2.6 + rb() * 1.1) * SCALE; // asterisms scale with the galaxy
      const ctrl = skel.map((s) =>
        base.pos
          .clone()
          .add(dir.clone().multiplyScalar(s[0] * SC))
          .add(perp.clone().multiplyScalar(s[1] * SC * side))
          .add(perp2.clone().multiplyScalar(s[2] * SC * 2.0)),
      );
      const curve = new THREE.CatmullRomCurve3(ctrl, false, "catmullrom", 0.5);

      /* NON-uniform spacing along the skeleton */
      let ts = [0];
      for (let i = 1; i < bd.k; i++) ts.push(ts[i - 1] + 0.5 + rb() * 0.9);
      const tm = ts[bd.k - 1];
      ts = ts.map((v) => 0.1 + (v / tm) * 0.88);

      const bn: StarNode[] = [];
      let prev = base;
      for (let j = 0; j < bd.k; j++) {
        const p = curve.getPointAt(Math.min(ts[j], 1)).clone();
        p.x += (rand() - 0.5) * 0.5 * SCALE;
        p.y += (rand() - 0.5) * 0.5 * SCALE;
        p.z += (rand() - 0.5) * 0.5 * SCALE;
        const isTip = j === bd.k - 1;
        const n = mk({
          type: isTip ? "merge" : "commit",
          branch: bd.n,
          birthT: Math.min(bd.at + (j + 1) * 0.014, 0.99),
          pos: p,
          sha: sha(),
          tier: isTip ? 3 : j % 2 === 0 ? 2 : 1,
          tw: rand() * 6.28,
          tws: 0.5 + rand() * 1.2,
        });
        bn.push(n);
        edges.push({ a: prev.id, b: n.id, kind: j === 0 ? "stem" : "branch" });
        prev = n;
      }
      /* merge line home — dotted */
      let mergeTo = main[main.length - 1];
      for (const m of main)
        if (m.birthT >= bn[bn.length - 1].birthT) {
          mergeTo = m;
          break;
        }
      edges.push({ a: bn[bn.length - 1].id, b: mergeTo.id, kind: "merge" });

      /* Bayer by brightness */
      [...bn]
        .sort((x, y) => y.tier - x.tier)
        .forEach((n, i) => (n.bayer = GREEK[i] || ""));
      branches.push({
        ...bd,
        archKey,
        nodes: bn,
        base,
        alpha: bn.find((n) => n.bayer === "α")!,
      });
    });

    const byId: Record<number, StarNode> = Object.fromEntries(
      nodes.map((n) => [n.id, n]),
    );
    const edgeBirth = (e: { a: number; b: number }) =>
      Math.max(byId[e.a].birthT, byId[e.b].birthT);

    /* ─────────── renderer / scene ─────────── */
    const scene = new THREE.Scene();
    scene.background = new THREE.Color(0x03050a);
    scene.fog = new THREE.FogExp2(0x05080f, 0.0125 / SCALE); // fog distance scales with the galaxy

    const camera = new THREE.PerspectiveCamera(
      52,
      W() / H(),
      0.1,
      600 * Math.max(1, SCALE),
    );
    camera.position.set(0, -6 * SCALE, 16 * SCALE);

    const renderer = new THREE.WebGLRenderer({ antialias: true, alpha: true });
    renderer.setPixelRatio(Math.min(devicePixelRatio, 2));
    renderer.setSize(W(), H());
    renderer.domElement.className = "three";
    wrap.insertBefore(renderer.domElement, wrap.firstChild);
    glCanvas = renderer.domElement;

    const labelsEl =
      $("#labels") ??
      (() => {
        const d = document.createElement("div");
        d.id = "labels";
        wrap.appendChild(d);
        return d;
      })();
    const labelRenderer = new CSS2DRenderer({ element: labelsEl });
    labelRenderer.setSize(W(), H());

    const controls = new OrbitControls(camera, renderer.domElement);
    controls.enableDamping = true;
    controls.dampingFactor = 0.05;
    controls.rotateSpeed = 0.45;
    controls.zoomSpeed = 0.7;
    controls.minDistance = 6 * SCALE;
    controls.maxDistance = 150 * SCALE;
    controls.enablePan = false;
    controls.enableRotate = interactive;
    controls.enableZoom = interactive;
    // Embedded previews spin the galaxy slowly around its own face (the view
    // axis) so it stays a face-on spiral — NOT OrbitControls.autoRotate, which
    // orbits around the vertical axis and flattens the disc to harsh edge-on
    // streaks.
    const spinRate = interactive ? 0 : (options.autoRotate ?? 0.5) * 0.12;

    const composer = new EffectComposer(renderer);
    composer.addPass(new RenderPass(scene, camera));
    const bloom = new UnrealBloomPass(
      new THREE.Vector2(W(), H()),
      interactive ? 0.95 : 0.62, // calmer bloom in the small embedded previews
      0.75,
      0.22,
    );
    composer.addPass(bloom);
    composer.addPass(new OutputPass());

    /* ─────────── GASEOUS NEBULA ─────────── */
    function nebulaTexture(seedN: number, tint: number[]): THREE.CanvasTexture {
      const S = 512,
        c = document.createElement("canvas");
      c.width = c.height = S;
      const g = c.getContext("2d")!,
        img = g.createImageData(S, S),
        d = img.data;
      const r = rng(seedN);
      const grid = new Float32Array(64 * 64);
      for (let i = 0; i < grid.length; i++) grid[i] = r();
      const at = (x: number, y: number) => grid[(y & 63) * 64 + (x & 63)];
      const sm = (t: number) => t * t * (3 - 2 * t);
      const noise = (x: number, y: number) => {
        const xi = Math.floor(x),
          yi = Math.floor(y),
          xf = x - xi,
          yf = y - yi;
        const u = sm(xf),
          v = sm(yf);
        return (
          at(xi, yi) * (1 - u) * (1 - v) +
          at(xi + 1, yi) * u * (1 - v) +
          at(xi, yi + 1) * (1 - u) * v +
          at(xi + 1, yi + 1) * u * v
        );
      };
      const fbm = (x: number, y: number) => {
        let s = 0,
          a = 0.5,
          f = 1;
        for (let o = 0; o < 6; o++) {
          s += a * noise(x * f, y * f);
          f *= 2.04;
          a *= 0.5;
        }
        return s;
      };
      for (let y = 0; y < S; y++)
        for (let x = 0; x < S; x++) {
          const nx = (x / S) * 7,
            ny = (y / S) * 7;
          let v = fbm(
            nx + fbm(nx * 0.5, ny * 0.5) * 2.2,
            ny + fbm(nx * 0.5 + 5, ny * 0.5 + 5) * 2.2,
          ); // domain-warped = wispy
          v = Math.pow(Math.max(v - 0.25, 0) / 0.75, 1.8);
          const dx = (x / S - 0.5) * 2,
            dy = (y / S - 0.5) * 2;
          const fall = Math.max(0, 1 - Math.sqrt(dx * dx + dy * dy));
          const a = v * Math.pow(fall, 1.7) * 255;
          const i = (y * S + x) * 4;
          d[i] = tint[0];
          d[i + 1] = tint[1];
          d[i + 2] = tint[2];
          d[i + 3] = a;
        }
      g.putImageData(img, 0, 0);
      const t = new THREE.CanvasTexture(c);
      t.colorSpace = THREE.SRGBColorSpace;
      return t;
    }
    const NEB_TINTS = [
      [92, 126, 220],
      [140, 96, 214],
      [64, 168, 190],
      [210, 140, 110],
      [96, 110, 196],
      [116, 84, 180],
    ];
    interface NebUserData {
      baseOp: number;
      spin: number;
      drift: number;
      dspeed: number;
      home: THREE.Vector3;
    }
    const nebulas: THREE.Sprite[] = [];
    for (let i = 0; i < 16; i++) {
      const tint = NEB_TINTS[(SEED + i * 7) % NEB_TINTS.length]; // colour set varies per repo
      const tex = nebulaTexture(SEED + 9000 + i * 137, tint); // cloud shapes vary per repo
      const m = new THREE.SpriteMaterial({
        map: tex,
        transparent: true,
        opacity: 0,
        blending: THREE.AdditiveBlending,
        depthWrite: false,
        depthTest: true,
      });
      const s = new THREE.Sprite(m);
      const a = rand() * Math.PI * 2,
        rr = (6 + rand() * 30) * SCALE;
      s.position.set(Math.cos(a) * rr, Math.sin(a) * rr * 0.8, (rand() - 0.5) * 22 * SCALE);
      const sz = (20 + rand() * 34) * SCALE;
      s.scale.set(sz, sz * (0.7 + rand() * 0.5), 1);
      s.userData = {
        baseOp: 0.16 + rand() * 0.2,
        spin: (rand() - 0.5) * 0.02,
        drift: rand() * 6.28,
        dspeed: 0.05 + rand() * 0.09,
        home: s.position.clone(),
      } satisfies NebUserData;
      scene.add(s);
      nebulas.push(s);
    }
    /* a warm core glow behind the genesis star */
    const coreNeb = new THREE.Sprite(
      new THREE.SpriteMaterial({
        map: nebulaTexture(SEED + 4242, [255, 210, 150]),
        transparent: true,
        opacity: 0,
        blending: THREE.AdditiveBlending,
        depthWrite: false,
      }),
    );
    coreNeb.scale.set(26 * SCALE, 26 * SCALE, 1);
    scene.add(coreNeb);

    /* deep starfield */
    function starfield(
      count: number,
      spread: number,
      size: number,
      op: number,
      seedN: number,
    ): THREE.Points {
      const r = rng(seedN),
        pos = new Float32Array(count * 3);
      for (let i = 0; i < count; i++) {
        const th = r() * Math.PI * 2,
          ph = Math.acos(2 * r() - 1),
          rr = (42 + r() * 140) * SCALE;
        pos[i * 3] = Math.sin(ph) * Math.cos(th) * rr;
        pos[i * 3 + 1] = Math.sin(ph) * Math.sin(th) * rr;
        pos[i * 3 + 2] = Math.cos(ph) * rr * 0.6;
      }
      const g = new THREE.BufferGeometry();
      g.setAttribute("position", new THREE.BufferAttribute(pos, 3));
      return new THREE.Points(
        g,
        new THREE.PointsMaterial({
          size,
          sizeAttenuation: false,
          color: 0xbacbe2,
          transparent: true,
          opacity: op,
          depthWrite: false,
          blending: THREE.AdditiveBlending,
        }),
      );
    }
    scene.add(
      starfield(1400, 1, 1.4, 0.55, SEED + 77),
      starfield(500, 1, 2.2, 0.25, SEED + 991),
    );

    /* ─────────── star glyphs ─────────── */
    function starTex(tier: number): THREE.CanvasTexture {
      const S = 256,
        c = document.createElement("canvas");
      c.width = c.height = S;
      const g = c.getContext("2d")!,
        m = S / 2;
      const coreR = [2.5, 4, 7, 11, 16][tier],
        haloR = [12, 22, 40, 64, 96][tier];
      const warm = tier === 4;
      const halo = g.createRadialGradient(m, m, 0, m, m, haloR);
      halo.addColorStop(0, warm ? "rgba(255,238,205,0.92)" : "rgba(228,240,255,0.85)");
      halo.addColorStop(0.2, warm ? "rgba(255,206,140,0.34)" : "rgba(186,210,246,0.30)");
      halo.addColorStop(1, warm ? "rgba(240,170,90,0)" : "rgba(150,180,230,0)");
      g.fillStyle = halo;
      g.beginPath();
      g.arc(m, m, haloR, 0, 6.284);
      g.fill();
      if (tier >= 2) {
        const len = tier >= 4 ? 122 : tier === 3 ? 92 : 56;
        g.save();
        g.translate(m, m);
        g.lineCap = "round";
        const spikes = tier >= 4 ? 6 : 4;
        for (let k = 0; k < spikes; k++) {
          const a = (k * Math.PI * 2) / spikes + (tier === 3 ? Math.PI / 4 : 0);
          const gr = g.createLinearGradient(0, 0, Math.cos(a) * len, Math.sin(a) * len);
          gr.addColorStop(0, warm ? "rgba(255,246,225,0.95)" : "rgba(245,250,255,0.95)");
          gr.addColorStop(1, "rgba(200,220,255,0)");
          g.strokeStyle = gr;
          g.lineWidth = tier >= 4 ? 3 : tier === 3 ? 2.2 : 1.5;
          g.beginPath();
          g.moveTo(0, 0);
          g.lineTo(Math.cos(a) * len, Math.sin(a) * len);
          g.stroke();
        }
        g.restore();
      }
      const core = g.createRadialGradient(m, m, 0, m, m, coreR);
      core.addColorStop(0, "rgba(255,255,255,1)");
      core.addColorStop(1, warm ? "rgba(255,240,215,0.7)" : "rgba(236,244,255,0.65)");
      g.fillStyle = core;
      g.beginPath();
      g.arc(m, m, coreR, 0, 6.284);
      g.fill();
      const t = new THREE.CanvasTexture(c);
      t.colorSpace = THREE.SRGBColorSpace;
      return t;
    }
    const TEX = [0, 1, 2, 3, 4].map(starTex);
    const TSIZE = [0.55, 0.85, 1.35, 2.1, 3.6];

    const starSprites = nodes.map((n) => {
      const s = new THREE.Sprite(
        new THREE.SpriteMaterial({
          map: TEX[n.tier],
          transparent: true,
          opacity: 0,
          blending: THREE.AdditiveBlending,
          depthWrite: false,
        }),
      );
      s.position.copy(n.pos);
      s.scale.setScalar(0.001);
      scene.add(s);
      return s;
    });

    /* ─────────── EDGES: solid trunk + dotted branch/merge ─────────── */
    function densify(a: THREE.Vector3, b: THREE.Vector3, per = 22): THREE.Vector3[] {
      const out: THREE.Vector3[] = [];
      for (let k = 0; k <= per; k++) out.push(a.clone().lerp(b, k / per));
      return out;
    }
    const edgeObjs = edges.map((e) => {
      const A = byId[e.a].pos,
        B = byId[e.b].pos;
      const dotted = e.kind === "merge" || e.kind === "stem";
      const isTrunk = e.kind === "trunk";
      const geo = new LineGeometry();
      geo.setPositions([A.x, A.y, A.z, A.x, A.y, A.z]);
      const mat = new LineMaterial({
        color: isTrunk ? 0xbbd4f5 : dotted ? 0x74a6e0 : 0xa8c4e8,
        // embedded previews use thinner, dimmer connective lines so the stars
        // and spiral read instead of a web of blown-out wires
        linewidth: interactive ? (isTrunk ? 1.9 : 1.2) : isTrunk ? 1.05 : 0.85,
        transparent: true,
        opacity: interactive ? (isTrunk ? 0.75 : 0.55) : isTrunk ? 0.5 : 0.4,
        dashed: dotted,
        dashSize: 0.3,
        gapSize: 0.42,
        blending: THREE.AdditiveBlending,
        depthWrite: false,
      });
      mat.resolution.set(W(), H()); // container size, not the window
      const line = new Line2(geo, mat);
      line.visible = false;
      scene.add(line);
      return { e, line, path: densify(A, B), dotted, birth: edgeBirth(e) };
    });

    /* ─────────── FLOWING DOTS along every connection ─────────── */
    const FLOW = 1500;
    const flowPos = new Float32Array(FLOW * 3);
    const flowAlpha = new Float32Array(FLOW);
    const flowGeo = new THREE.BufferGeometry();
    flowGeo.setAttribute("position", new THREE.BufferAttribute(flowPos, 3));
    flowGeo.setAttribute("aAlpha", new THREE.BufferAttribute(flowAlpha, 1));
    const flowMat = new THREE.ShaderMaterial({
      transparent: true,
      depthWrite: false,
      blending: THREE.AdditiveBlending,
      uniforms: { uSize: { value: 3.4 * Math.min(devicePixelRatio, 2) } },
      vertexShader: `
        attribute float aAlpha; varying float vA;
        uniform float uSize;
        void main(){ vA=aAlpha;
          vec4 mv = modelViewMatrix * vec4(position,1.0);
          gl_PointSize = uSize * (30.0 / -mv.z);
          gl_Position = projectionMatrix * mv; }`,
      fragmentShader: `
        varying float vA;
        void main(){
          vec2 d = gl_PointCoord - vec2(0.5);
          float r = length(d);
          if(r>0.5) discard;
          float a = smoothstep(0.5, 0.0, r) * vA;
          gl_FragColor = vec4(vec3(0.80,0.90,1.0), a); }`,
    });
    const flow = new THREE.Points(flowGeo, flowMat);
    scene.add(flow);

    const flowParts: Array<{ ei: number; t: number; speed: number }> = [];
    for (let i = 0; i < FLOW; i++) {
      const ei = Math.floor(rand() * edgeObjs.length);
      flowParts.push({ ei, t: rand(), speed: 0.1 + rand() * 0.22 });
    }

    /* ─────────── labels ─────────── */
    function mkLabel(
      html: string,
      cls: string,
      pos: THREE.Vector3,
      off?: THREE.Vector3,
    ): HTMLDivElement {
      const d = document.createElement("div");
      d.className = "lbl " + cls;
      d.innerHTML = html;
      d.style.opacity = "0";
      const o = new CSS2DObject(d);
      o.position.copy(pos).add(off || new THREE.Vector3());
      if (wantLabels) scene.add(o); // preview mode keeps the div (apply() still
      // pokes its opacity harmlessly) but never mounts it into the scene
      return d;
    }
    const genesisEl = mkLabel(
      `Genesis<span class="cat">${REPO} · initial commit</span>`,
      "genesis",
      new THREE.Vector3(0, 0, 0),
      new THREE.Vector3(0, -2.6, 0),
    );
    const branchEls = branches.map((b) =>
      mkLabel(
        `${b.n}<span class="cat">${b.sub}</span>`,
        "name",
        b.alpha.pos,
        new THREE.Vector3(0, -1.7, 0),
      ),
    );
    const bayerEls = branches.flatMap((b) =>
      b.nodes
        .filter((n) => n.tier >= 2)
        .map((n) => ({
          el: mkLabel(n.bayer!, "bayer", n.pos, new THREE.Vector3(0.75, 0.55, 0)),
          n,
        })),
    );

    /* corner ticks on the frame (standalone /map only) */
    if (wantHud)
      (["nw", "ne", "sw", "se"] as const).forEach((k) => {
        const d = document.createElement("div");
        d.className = "cnr";
        const s = {
          nw: { top: "22px", left: "22px", borderTopWidth: "1px", borderLeftWidth: "1px" },
          ne: { top: "22px", right: "22px", borderTopWidth: "1px", borderRightWidth: "1px" },
          sw: { bottom: "22px", left: "22px", borderBottomWidth: "1px", borderLeftWidth: "1px" },
          se: { bottom: "22px", right: "22px", borderBottomWidth: "1px", borderRightWidth: "1px" },
        }[k];
        Object.assign(d.style, s);
        wrap.appendChild(d);
      });

    /* ─────────── growth choreography ─────────── */
    const ease = {
      out: (t: number) => 1 - Math.pow(1 - t, 3),
      inOut: (t: number) => (t < 0.5 ? 4 * t * t * t : 1 - Math.pow(-2 * t + 2, 3) / 2),
      back: (t: number) => {
        const c = 2.70158;
        return 1 + (c + 1) * Math.pow(t - 1, 3) + c * Math.pow(t - 1, 2);
      },
    };
    const seg = (t: number, a: number, b: number) =>
      Math.max(0, Math.min(1, (t - a) / (b - a)));

    const IGNITE = 1.9; // genesis star ignites first
    const GROW = scale.duration; // reveal duration scales mildly with repo size
    const DURATION = IGNITE + GROW + 1.6;

    let time = 0,
      playing = options.autoplay !== false,
      visible = true,
      last = performance.now();
    // hero preview: keep the clock running past DURATION so twinkle / flow /
    // nebula drift stay alive at "present day" (all growth segs are saturated).
    const keepAlive = !interactive && options.autoplay !== false;
    const scrubEl = $<HTMLInputElement>("#scrub"),
      readEl = $("#readout");

    function apply(t: number) {
      const ig = ease.out(seg(t, 0, IGNITE));
      const prog = ease.inOut(seg(t, IGNITE, IGNITE + GROW)); // 0→1 reveal by birthT

      /* nebula breathes in during ignition, then drifts forever */
      nebulas.forEach((s, i) => {
        const u = s.userData as NebUserData;
        s.material.opacity = u.baseOp * ease.out(seg(t, 0.2 + i * 0.045, 2.6 + i * 0.05));
        s.material.rotation += u.spin * 0.016;
        s.position.x = u.home.x + Math.sin(t * u.dspeed + u.drift) * 1.5 * SCALE;
        s.position.y = u.home.y + Math.cos(t * u.dspeed * 0.8 + u.drift) * 1.2 * SCALE;
      });
      coreNeb.material.opacity = 0.42 * ig * (0.85 + 0.15 * Math.sin(t * 0.7));
      coreNeb.scale.setScalar(26 * SCALE * (0.75 + 0.25 * ig));

      /* genesis ignites, then the rest grow by birthT */
      nodes.forEach((n, i) => {
        let f;
        if (n.type === "genesis") {
          f = ig;
        } else {
          const b = n.birthT;
          f = seg(prog, Math.max(b - 0.035, 0), b + 0.02);
        }
        const e = ease.back(Math.min(f, 1));
        const tw = 0.85 + 0.15 * Math.sin(t * n.tws * 2.2 + n.tw);
        starSprites[i].material.opacity = Math.min(f * 1.2, 1) * tw;
        starSprites[i].scale.setScalar(Math.max(0.001, TSIZE[n.tier] * e));
      });
      /* genesis pulse */
      const gp = 1 + Math.sin(t * 1.1) * 0.06;
      starSprites[0].scale.setScalar(
        TSIZE[4] * ig * gp * (1 + Math.sin(ease.out(seg(t, 0, IGNITE)) * Math.PI) * 0.5),
      );

      /* edges draw progressively */
      edgeObjs.forEach((o) => {
        const f = seg(prog, Math.max(o.birth - 0.045, 0), o.birth + 0.01);
        if (f <= 0.001) {
          o.line.visible = false;
          return;
        }
        o.line.visible = true;
        const n = Math.max(2, Math.floor(o.path.length * Math.min(f, 1)));
        const sl = o.path.slice(0, n);
        o.line.geometry.setPositions(sl.flatMap((p) => [p.x, p.y, p.z]));
        if (o.dotted) o.line.computeLineDistances();
      });

      /* flowing dots ride the revealed edges */
      const dt = 0.016;
      for (let i = 0; i < FLOW; i++) {
        const fp = flowParts[i],
          o = edgeObjs[fp.ei];
        const revealed = seg(prog, Math.max(o.birth - 0.045, 0), o.birth + 0.01) > 0.98;
        fp.t += fp.speed * dt;
        if (fp.t > 1) {
          fp.t -= 1;
        }
        const A = byId[o.e.a].pos,
          B = byId[o.e.b].pos;
        const x = A.x + (B.x - A.x) * fp.t,
          y = A.y + (B.y - A.y) * fp.t,
          z = A.z + (B.z - A.z) * fp.t;
        flowPos[i * 3] = x;
        flowPos[i * 3 + 1] = y;
        flowPos[i * 3 + 2] = z;
        flowAlpha[i] = revealed ? 0.3 + 0.7 * Math.sin(fp.t * Math.PI) : 0;
      }
      flowGeo.attributes.position.needsUpdate = true;
      flowGeo.attributes.aAlpha.needsUpdate = true;

      /* labels */
      genesisEl.style.opacity = String(0.95 * ease.out(seg(t, IGNITE * 0.55, IGNITE + 0.5)));
      branches.forEach((b, i) => {
        const f = seg(prog, b.alpha.birthT + 0.01, b.alpha.birthT + 0.07);
        branchEls[i].style.opacity = String(0.92 * f);
        branchEls[i].style.letterSpacing = 1.2 + (1 - f) * 6 + "px";
      });
      bayerEls.forEach(({ el, n }) => {
        el.style.opacity = String(0.8 * seg(prog, n.birthT + 0.005, n.birthT + 0.05));
      });

      /* camera pulls back as the galaxy grows (framing scales with the galaxy) */
      const want = (15 + prog * 30) * SCALE;
      const cur = camera.position.length();
      camera.position.multiplyScalar(1 + ((want - cur) / cur) * 0.02);

      if (readEl)
        readEl.textContent =
          t < IGNITE
            ? "igniting genesis"
            : prog < 1
              ? `growing · ${Math.round(prog * 100)}%`
              : "present day";
    }

    function loop(now: number) {
      if (destroyed) return;
      const dt = Math.min((now - last) / 1000, 0.05);
      last = now;
      if (playing) {
        time += dt;
        if (time >= DURATION && !keepAlive) {
          time = DURATION;
          playing = false;
        }
        if (scrubEl)
          scrubEl.value = String(
            Math.round((Math.min(time, DURATION) / DURATION) * 1000),
          );
      }
      if (spinRate) scene.rotation.z += spinRate * dt; // face-on galaxy spin
      controls.update();
      if (visible) {
        apply(time);
        composer.render();
        labelRenderer.render(scene, camera);
      }
      rafId = requestAnimationFrame(loop);
    }
    const replayEl = $<HTMLButtonElement>("#replay");
    if (replayEl)
      replayEl.onclick = () => {
        time = 0;
        playing = true;
        camera.position.setLength(16 * SCALE);
      };
    if (scrubEl)
      scrubEl.oninput = (e) => {
        time = (+(e.target as HTMLInputElement).value / 1000) * DURATION;
        playing = false;
      };

    // control surface for embedded previews (hero autoplay / legend scrub)
    options.onReady?.({
      seek: (p: number) => {
        time = Math.max(0, Math.min(1, p)) * DURATION;
        playing = false;
      },
      play: () => {
        playing = true;
      },
      pause: () => {
        playing = false;
      },
    });

    // pause rendering while the container is offscreen (two WebGL previews on
    // one page: only the visible one should draw)
    const vio = new IntersectionObserver(
      (es) => es.forEach((e) => (visible = e.isIntersecting)),
      { threshold: 0.01 },
    );
    vio.observe(wrap);

    onResize = () => {
      camera.aspect = W() / H();
      camera.updateProjectionMatrix();
      renderer.setSize(W(), H());
      labelRenderer.setSize(W(), H());
      composer.setSize(W(), H());
      edgeObjs.forEach((o) => o.line.material.resolution.set(W(), H()));
    };
    addEventListener("resize", onResize);
    let ro: ResizeObserver | null = null;
    if (sizeToC) {
      ro = new ResizeObserver(onResize);
      ro.observe(wrap);
    }

    // ---- register teardown for GPU + DOM resources ----
    gpuDisposers.push(() => {
      controls.dispose();
      composer.dispose();
      nebulas.forEach((s) => {
        (s.material.map as THREE.Texture | null)?.dispose();
        s.material.dispose();
      });
      (coreNeb.material.map as THREE.Texture | null)?.dispose();
      coreNeb.material.dispose();
      TEX.forEach((t) => t.dispose());
      starSprites.forEach((s) => s.material.dispose());
      edgeObjs.forEach((o) => {
        o.line.geometry.dispose();
        o.line.material.dispose();
      });
      flowGeo.dispose();
      flowMat.dispose();
      scene.traverse((obj) => {
        const anyObj = obj as THREE.Points | THREE.Mesh;
        if ((anyObj as THREE.Points).geometry) (anyObj as THREE.Points).geometry.dispose?.();
      });
      renderer.dispose();
      vio.disconnect();
      ro?.disconnect();
      labelsEl.innerHTML = "";
      wrap.querySelectorAll(".cnr").forEach((el) => el.remove());
    });

    rafId = requestAnimationFrame(loop);
  })();

  return () => {
    destroyed = true;
    if (rafId) cancelAnimationFrame(rafId);
    if (onResize) removeEventListener("resize", onResize);
    gpuDisposers.forEach((d) => {
      try {
        d();
      } catch {}
    });
    if (glCanvas && glCanvas.parentNode) glCanvas.parentNode.removeChild(glCanvas);
  };
}

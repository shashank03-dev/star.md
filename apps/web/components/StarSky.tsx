"use client";

import { useEffect, useRef } from "react";

// The landing's night sky, built to read as a photograph rather than a pattern:
// - a Milky Way band of value-noise dust with darker lanes and faint colour,
// - ~1,600 stars with a real magnitude distribution (many faint, few bright)
//   and black-body colours from blue-white to orange,
// - the brightest handful carry soft diffraction spikes, echoing the map,
// - only bright stars scintillate; the faint field is baked into three parallax
//   layers (drawn once, blitted per frame) so the loop stays cheap.
// Cursor and scroll move the layers at different depths; a rare meteor crosses.

type RGB = [number, number, number];

// approximate stellar colours by spectral class, weighted by how common they
// look to the eye (white/yellow-white dominate, deep orange is rare)
const TEMPS: Array<[RGB, number]> = [
  [[155, 176, 255], 0.1],
  [[190, 206, 255], 0.18],
  [[232, 238, 255], 0.3],
  [[255, 246, 234], 0.22],
  [[255, 228, 196], 0.13],
  [[255, 204, 150], 0.07],
];

const rng = (s: number) => {
  let a = s >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
};

const pickTemp = (r: number): RGB => {
  let acc = 0;
  for (const [c, w] of TEMPS) {
    acc += w;
    if (r < acc) return c;
  }
  return TEMPS[2][0];
};

// smooth 2D value noise + fbm for the galactic dust
function makeNoise(seed: number) {
  const r = rng(seed);
  const P = new Uint8Array(512);
  const G = new Float32Array(256);
  for (let i = 0; i < 256; i++) {
    P[i] = i;
    G[i] = r();
  }
  for (let i = 255; i > 0; i--) {
    const j = (r() * (i + 1)) | 0;
    [P[i], P[j]] = [P[j], P[i]];
  }
  for (let i = 0; i < 256; i++) P[i + 256] = P[i];
  const v = (x: number, y: number) => G[P[(P[x & 255] + y) & 255]];
  const s = (t: number) => t * t * (3 - 2 * t);
  const n = (x: number, y: number) => {
    const xi = Math.floor(x),
      yi = Math.floor(y);
    const xf = s(x - xi),
      yf = s(y - yi);
    const a = v(xi, yi),
      b = v(xi + 1, yi),
      c = v(xi, yi + 1),
      d = v(xi + 1, yi + 1);
    return a + (b - a) * xf + (c - a) * yf + (a - b - c + d) * xf * yf;
  };
  return (x: number, y: number, oct = 5) => {
    let f = 0,
      amp = 0.5,
      fr = 1;
    for (let i = 0; i < oct; i++) {
      f += amp * n(x * fr, y * fr);
      fr *= 2.03;
      amp *= 0.5;
    }
    return f;
  };
}

interface Bright {
  x: number;
  y: number;
  layer: number;
  r: number; // core radius, px
  a: number; // base alpha
  c: RGB;
  tw: number;
  ts: number;
  spikes: boolean;
}

export default function StarSky() {
  const canvasRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const cv = canvasRef.current;
    if (!cv) return;
    const ctx = cv.getContext("2d");
    if (!ctx) return;

    const reduce = matchMedia("(prefers-reduced-motion:reduce)").matches;
    const DPR = Math.min(devicePixelRatio || 1, 2);
    const DEPTH = [0.12, 0.3, 0.6];
    let W = 0,
      H = 0;
    let layers: HTMLCanvasElement[] = [];
    let band: HTMLCanvasElement | null = null;
    let brights: Bright[] = [];

    // one soft sprite per colour, reused for every bright star
    const sprites = new Map<string, HTMLCanvasElement>();
    const sprite = (c: RGB) => {
      const k = c.join();
      let s = sprites.get(k);
      if (s) return s;
      s = document.createElement("canvas");
      s.width = s.height = 64;
      const g = s.getContext("2d")!;
      const grd = g.createRadialGradient(32, 32, 0, 32, 32, 32);
      grd.addColorStop(0, "rgba(255,255,255,1)");
      grd.addColorStop(0.08, `rgba(${c},0.95)`);
      grd.addColorStop(0.22, `rgba(${c},0.32)`);
      grd.addColorStop(0.5, `rgba(${c},0.07)`);
      grd.addColorStop(1, `rgba(${c},0)`);
      g.fillStyle = grd;
      g.fillRect(0, 0, 64, 64);
      sprites.set(k, s);
      return s;
    };

    function buildBand() {
      // the Milky Way, rendered at quarter resolution and scaled up (dust is soft)
      // 12% oversize so cursor parallax never exposes an edge (it never wraps)
      const q = 4;
      const bw = Math.ceil((W * 1.12) / q),
        bh = Math.ceil((H * 1.12) / q);
      const c = document.createElement("canvas");
      c.width = bw;
      c.height = bh;
      const g = c.getContext("2d")!;
      const img = g.createImageData(bw, bh);
      const fbm = makeNoise(7);
      const ang = -0.42; // band runs low-left to upper-right
      const ca = Math.cos(ang),
        sa = Math.sin(ang);
      for (let y = 0; y < bh; y++) {
        for (let x = 0; x < bw; x++) {
          const nx = x / bw - 0.5,
            ny = (y / bh - 0.42) * (bh / bw);
          const u = nx * ca - ny * sa; // along the band
          const v = nx * sa + ny * ca; // across it
          const core = Math.exp(-(v * v) / 0.012);
          const halo = Math.exp(-(v * v) / 0.05);
          const d = fbm(u * 5 + 3, v * 9 + 1);
          const lanes = Math.max(0, fbm(u * 11 + 9, v * 22 + 4) - 0.48) * 2.6;
          let lum = (core * 0.75 + halo * 0.35) * (0.45 + d) - core * lanes * 0.9;
          lum = Math.max(0, lum) * 0.34;
          const warm = fbm(u * 2 + 20, v * 2) ;
          const i = (y * bw + x) * 4;
          img.data[i] = 90 + 120 * warm;
          img.data[i + 1] = 100 + 80 * warm;
          img.data[i + 2] = 150 + 60 * (1 - warm);
          img.data[i + 3] = Math.min(255, lum * 255);
        }
      }
      g.putImageData(img, 0, 0);
      band = c;
    }

    function buildStars() {
      const r = rng(90210);
      const area = (W * H) / (1440 * 900);
      const total = Math.round(1700 * Math.min(1.6, Math.max(0.45, area)));
      layers = DEPTH.map(() => {
        const c = document.createElement("canvas");
        c.width = W * DPR;
        c.height = H * DPR;
        const g = c.getContext("2d")!;
        g.scale(DPR, DPR);
        return c;
      });
      const gs = layers.map((c) => c.getContext("2d")!);
      brights = [];
      let spiked = 0;
      for (let i = 0; i < total; i++) {
        // concentrate a share of the faint stars along the galactic band
        let x = r(),
          y = r();
        if (r() < 0.35) {
          const t = r() - 0.5;
          const off = (r() + r() + r() - 1.5) * 0.09;
          x = 0.5 + t * 1.2 * Math.cos(-0.42) - off * Math.sin(-0.42);
          y = 0.42 + t * 1.2 * Math.sin(-0.42) * (W / H) + off * Math.cos(-0.42) * (W / H);
          if (x < 0 || x > 1 || y < 0 || y > 1) continue;
        }
        const m = Math.pow(r(), 7.5); // 0 = faint .. 1 = brilliant
        const layer = m > 0.3 ? 2 : r() < 0.55 ? 0 : 1;
        const c = pickTemp(r());
        if (m > 0.2) {
          brights.push({
            x,
            y,
            layer,
            r: 0.6 + m * 1.9,
            a: 0.45 + m * 0.55,
            c,
            tw: r() * 6.283,
            ts: 0.6 + r() * 1.8,
            spikes: m > 0.9 && spiked++ < 6,
          });
        } else {
          const g = gs[layer];
          g.globalAlpha = Math.min(1, 0.16 + m * 3.6);
          g.fillStyle = `rgb(${c})`;
          g.beginPath();
          g.arc(x * W, y * H, 0.3 + m * 3, 0, 6.283);
          g.fill();
        }
      }
    }

    function resize() {
      W = innerWidth;
      H = innerHeight;
      cv!.width = W * DPR;
      cv!.height = H * DPR;
      cv!.style.width = W + "px";
      cv!.style.height = H + "px";
      ctx!.setTransform(DPR, 0, 0, DPR, 0, 0);
      buildBand();
      buildStars();
    }
    resize();
    let rT = 0;
    const onResize = () => {
      clearTimeout(rT);
      rT = window.setTimeout(() => {
        resize();
        if (reduce) requestAnimationFrame(frame);
      }, 150);
    };
    addEventListener("resize", onResize);

    let tmx = 0,
      tmy = 0,
      pmx = 0,
      pmy = 0;
    const onMouse = (e: MouseEvent) => {
      tmx = e.clientX / innerWidth - 0.5;
      tmy = e.clientY / innerHeight - 0.5;
    };
    if (!reduce) addEventListener("mousemove", onMouse, { passive: true });

    // blit a layer with wrap-around offset
    const blit = (img: HTMLCanvasElement, ox: number, oy: number, alpha = 1) => {
      ox = ((ox % W) + W) % W;
      oy = ((oy % H) + H) % H;
      ctx!.globalAlpha = alpha;
      for (const dx of [ox - W, ox])
        for (const dy of [oy - H, oy]) ctx!.drawImage(img, dx, dy, W, H);
    };

    let meteor: { x: number; y: number; vx: number; vy: number; life: number; max: number } | null = null;
    let nextMeteor = 4000;

    let raf = 0;
    function frame(t: number) {
      pmx += (tmx - pmx) * 0.04;
      pmy += (tmy - pmy) * 0.04;
      const sc = scrollY || 0;

      const g = ctx!.createLinearGradient(0, 0, 0, H);
      g.addColorStop(0, "#03040b");
      g.addColorStop(0.6, "#02030a");
      g.addColorStop(1, "#010207");
      ctx!.globalCompositeOperation = "source-over";
      ctx!.globalAlpha = 1;
      ctx!.fillStyle = g;
      ctx!.fillRect(0, 0, W, H);

      ctx!.globalCompositeOperation = "lighter";
      if (band) {
        ctx!.globalAlpha = 1;
        ctx!.drawImage(band, -W * 0.06 - pmx * 10, -H * 0.06 - pmy * 8, W * 1.12, H * 1.12);
      }
      layers.forEach((L, i) => {
        const d = DEPTH[i];
        blit(L, pmx * -46 * d, pmy * -34 * d - sc * 0.16 * d);
      });

      for (const s of brights) {
        const d = DEPTH[s.layer];
        let x = s.x * W + pmx * -46 * d;
        let y = s.y * H + pmy * -34 * d - sc * 0.16 * d;
        x = ((x % W) + W) % W;
        y = ((y % H) + H) % H;
        const sci = reduce ? 1 : 0.82 + 0.18 * Math.sin(t * 0.001 * s.ts + s.tw) * Math.sin(t * 0.0023 * s.ts + s.tw * 2);
        const a = s.a * sci;
        const R = s.r * 4.2;
        ctx!.globalAlpha = a;
        ctx!.drawImage(sprite(s.c), x - R, y - R, R * 2, R * 2);
        if (s.spikes) {
          const L = s.r * 7 * (0.9 + 0.1 * sci);
          ctx!.globalAlpha = a * 0.4;
          for (const [dx, dy] of [
            [1, 0],
            [0, 1],
          ]) {
            const gr = ctx!.createLinearGradient(x - dx * L, y - dy * L, x + dx * L, y + dy * L);
            gr.addColorStop(0, `rgba(${s.c},0)`);
            gr.addColorStop(0.5, `rgba(255,255,255,0.9)`);
            gr.addColorStop(1, `rgba(${s.c},0)`);
            ctx!.strokeStyle = gr;
            ctx!.lineWidth = 0.8;
            ctx!.beginPath();
            ctx!.moveTo(x - dx * L, y - dy * L);
            ctx!.lineTo(x + dx * L, y + dy * L);
            ctx!.stroke();
          }
        }
      }

      if (!reduce) {
        if (!meteor && t > nextMeteor) {
          const r = Math.random;
          meteor = { x: r() * W * 0.7, y: r() * H * 0.35, vx: 0.9 + r() * 0.5, vy: 0.32 + r() * 0.2, life: 0, max: 700 + r() * 400 };
          nextMeteor = t + 7000 + r() * 11000;
        }
        if (meteor) {
          meteor.life += 16;
          const p = meteor.life / meteor.max;
          const hx = meteor.x + meteor.vx * meteor.life,
            hy = meteor.y + meteor.vy * meteor.life;
          const len = 180 * Math.sin(Math.min(p, 1) * Math.PI);
          const n = Math.hypot(meteor.vx, meteor.vy);
          const tx = hx - (meteor.vx / n) * len,
            ty = hy - (meteor.vy / n) * len;
          const fade = Math.sin(Math.min(p, 1) * Math.PI);
          const gr = ctx!.createLinearGradient(hx, hy, tx, ty);
          gr.addColorStop(0, `rgba(255,255,255,${0.9 * fade})`);
          gr.addColorStop(0.25, `rgba(200,220,255,${0.35 * fade})`);
          gr.addColorStop(1, "rgba(200,220,255,0)");
          ctx!.globalAlpha = 1;
          ctx!.strokeStyle = gr;
          ctx!.lineWidth = 1.3;
          ctx!.lineCap = "round";
          ctx!.beginPath();
          ctx!.moveTo(hx, hy);
          ctx!.lineTo(tx, ty);
          ctx!.stroke();
          ctx!.globalAlpha = fade;
          ctx!.drawImage(sprite([220, 232, 255]), hx - 8, hy - 8, 16, 16);
          if (p >= 1) meteor = null;
        }
      }
      ctx!.globalAlpha = 1;
      ctx!.globalCompositeOperation = "source-over";
      if (!reduce) raf = requestAnimationFrame(frame);
    }
    raf = requestAnimationFrame(frame);
    const onScroll = () => {
      if (reduce) requestAnimationFrame(frame);
    };
    addEventListener("scroll", onScroll, { passive: true });

    return () => {
      cancelAnimationFrame(raf);
      clearTimeout(rT);
      removeEventListener("resize", onResize);
      removeEventListener("mousemove", onMouse);
      removeEventListener("scroll", onScroll);
    };
  }, []);

  return <canvas id="sky" ref={canvasRef} aria-hidden="true" />;
}

"use client";

import { useEffect, useRef } from "react";

// The moving star sky behind the landing hero: layered parallax field + twinkle
// + occasional shooting star. Ported from landing.html; identical rng, layers
// and draw math, wrapped in a component with a proper teardown.
export default function StarSky() {
  const canvasRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const cv = canvasRef.current;
    if (!cv) return;
    const ctx = cv.getContext("2d");
    if (!ctx) return;

    const reduce = matchMedia("(prefers-reduced-motion:reduce)").matches;
    let W = 0,
      H = 0;
    const DPR = Math.min(devicePixelRatio || 1, 2);

    const rng = (s: number) => {
      let a = s >>> 0;
      return () => {
        a |= 0;
        a = (a + 0x6d2b79f5) | 0;
        let t = Math.imul(a ^ (a >>> 15), 1 | a);
        t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
        return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
      };
    };

    interface Star {
      x: number;
      y: number;
      r: number;
      tw: number;
      ts: number;
      c: number[];
      big: boolean;
    }
    interface Layer {
      depth: number;
      stars: Star[];
    }
    let LAYERS: Layer[] = [];
    function build() {
      const r = rng(90210);
      const cols = [
        [255, 255, 255],
        [201, 220, 255],
        [255, 236, 209],
        [178, 201, 255],
      ];
      LAYERS = [
        { n: 90, sp: 0.1, sz: 0.6 },
        { n: 80, sp: 0.28, sz: 1.0 },
        { n: 46, sz: 1.7, sp: 0.55 },
      ].map((L) => ({
        depth: L.sp,
        stars: Array.from({ length: L.n }, () => ({
          x: r(),
          y: r(),
          r: L.sz * (0.6 + r() * 1.1),
          tw: r() * 6.28,
          ts: 0.5 + r() * 1.4,
          c: cols[(r() * cols.length) | 0],
          big: r() > 0.86,
        })),
      }));
    }
    build();

    function resize() {
      W = innerWidth;
      H = innerHeight;
      cv!.width = W * DPR;
      cv!.height = H * DPR;
      cv!.style.width = W + "px";
      cv!.style.height = H + "px";
      ctx!.setTransform(DPR, 0, 0, DPR, 0, 0);
    }
    resize();
    addEventListener("resize", resize);

    let tmx = 0,
      tmy = 0,
      pmx = 0,
      pmy = 0;
    const onMouse = (e: MouseEvent) => {
      tmx = e.clientX / innerWidth - 0.5;
      tmy = e.clientY / innerHeight - 0.5;
    };
    if (!reduce) addEventListener("mousemove", onMouse);

    let shoot: {
      x: number;
      y: number;
      len: number;
      vx: number;
      vy: number;
      life: number;
      max: number;
    } | null = null;
    let nextShoot = 2000;
    function spawnShoot(t: number) {
      const r = Math.random;
      shoot = {
        x: r() * W * 0.8,
        y: r() * H * 0.4,
        len: 120 + r() * 160,
        vx: 6 + r() * 5,
        vy: 2.4 + r() * 2,
        life: 0,
        max: 520 + r() * 260,
      };
      nextShoot = t + 6000 + r() * 9000;
    }

    let rafId = 0;
    function frame(t: number) {
      pmx += (tmx - pmx) * 0.05;
      pmy += (tmy - pmy) * 0.05;
      const sc = scrollY || 0;
      const g = ctx!.createLinearGradient(0, 0, 0, H);
      g.addColorStop(0, "#070a16");
      g.addColorStop(0.5, "#05070f");
      g.addColorStop(1, "#04050a");
      ctx!.fillStyle = g;
      ctx!.fillRect(0, 0, W, H);

      ctx!.globalCompositeOperation = "lighter";
      for (const L of LAYERS) {
        const ox = pmx * 40 * L.depth,
          oy = pmy * 30 * L.depth - sc * 0.15 * L.depth;
        for (const s of L.stars) {
          let x = s.x * W + ox,
            y = (s.y * H + oy) % H;
          if (y < 0) y += H;
          const tw = reduce ? 0.85 : 0.55 + 0.45 * Math.sin(t * 0.001 * s.ts + s.tw);
          ctx!.globalAlpha = tw;
          if (s.big) {
            const h = ctx!.createRadialGradient(x, y, 0, x, y, s.r * 5);
            h.addColorStop(0, `rgba(${s.c[0]},${s.c[1]},${s.c[2]},.9)`);
            h.addColorStop(1, `rgba(${s.c[0]},${s.c[1]},${s.c[2]},0)`);
            ctx!.fillStyle = h;
            ctx!.beginPath();
            ctx!.arc(x, y, s.r * 5, 0, 6.28);
            ctx!.fill();
          }
          ctx!.fillStyle = `rgb(${s.c[0]},${s.c[1]},${s.c[2]})`;
          ctx!.beginPath();
          ctx!.arc(x, y, s.r, 0, 6.28);
          ctx!.fill();
        }
      }
      if (!reduce) {
        if (!shoot && t > nextShoot) spawnShoot(t);
        if (shoot) {
          shoot.life += 16;
          const p = shoot.life / shoot.max;
          const x = shoot.x + shoot.vx * shoot.life * 0.12,
            y = shoot.y + shoot.vy * shoot.life * 0.12;
          const a = Math.sin(Math.min(p, 1) * Math.PI);
          const gr = ctx!.createLinearGradient(
            x,
            y,
            x - shoot.len,
            y - shoot.len * (shoot.vy / shoot.vx),
          );
          gr.addColorStop(0, `rgba(255,255,255,${a})`);
          gr.addColorStop(1, "rgba(255,255,255,0)");
          ctx!.globalAlpha = 1;
          ctx!.strokeStyle = gr;
          ctx!.lineWidth = 1.6;
          ctx!.beginPath();
          ctx!.moveTo(x, y);
          ctx!.lineTo(x - shoot.len, y - shoot.len * (shoot.vy / shoot.vx));
          ctx!.stroke();
          if (p >= 1) shoot = null;
        }
      }
      ctx!.globalAlpha = 1;
      ctx!.globalCompositeOperation = "source-over";
      rafId = requestAnimationFrame(frame);
    }
    rafId = requestAnimationFrame(frame);

    return () => {
      cancelAnimationFrame(rafId);
      removeEventListener("resize", resize);
      removeEventListener("mousemove", onMouse);
    };
  }, []);

  return <canvas id="sky" ref={canvasRef} />;
}

"use client";

import { useEffect, useMemo, useRef } from "react";
import { buildGalaxy, drawGalaxy, FULL, type View } from "@/lib/starmap/preview";

// A lightweight, ALIVE stand-in for the real WebGL star map, shown inside the
// hero's floating window "screen". It renders the shared 2D galaxy model
// (lib/starmap/preview.ts) at full reveal — the same warm genesis core, spiral
// of magnitude-tiered stars with diffraction spikes, nebulae and flowing merge
// lines the real /map draws — but in a few KB of 2D canvas, no Three.js.
//
// Performance: capped at ~30fps, paused whenever it scrolls offscreen
// (IntersectionObserver), and under prefers-reduced-motion / `still` it draws a
// single static frame and never starts a loop.
export default function MapPreview({
  still = false,
  repo = "sindresorhus/slugify",
}: {
  still?: boolean;
  repo?: string;
}) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const galaxy = useMemo(() => buildGalaxy(repo), [repo]);

  useEffect(() => {
    const cv = canvasRef.current;
    if (!cv) return;
    const ctx = cv.getContext("2d");
    if (!ctx) return;

    const reduce =
      still ||
      (typeof matchMedia === "function" &&
        matchMedia("(prefers-reduced-motion:reduce)").matches);
    const DPR = Math.min(devicePixelRatio || 1, 2);

    let view: View = { w: 1, h: 1, cx: 0, cy: 0, scale: 1, rot: 0, bg: 1 };
    function resize() {
      const rect = cv!.getBoundingClientRect();
      const W = Math.max(1, rect.width);
      const H = Math.max(1, rect.height);
      cv!.width = Math.round(W * DPR);
      cv!.height = Math.round(H * DPR);
      ctx!.setTransform(DPR, 0, 0, DPR, 0, 0);
      view = {
        w: W,
        h: H,
        cx: W * 0.5,
        cy: H * 0.5,
        scale: Math.min(W, H) * 0.46,
        rot: 0,
        bg: 1,
      };
    }
    resize();
    const ro = new ResizeObserver(resize);
    ro.observe(cv);

    const frame = (t: number) => {
      view.rot = reduce ? 0.2 : t * 0.00004;
      drawGalaxy(ctx!, galaxy, view, FULL, reduce ? 6 : t * 0.001);
    };

    let rafId = 0;
    let last = 0;
    let running = false;
    const FRAME = 1000 / 30;
    function loop(now: number) {
      if (now - last >= FRAME) {
        last = now;
        frame(now);
      }
      rafId = requestAnimationFrame(loop);
    }
    function start() {
      if (running || reduce) return;
      running = true;
      rafId = requestAnimationFrame(loop);
    }
    function stop() {
      running = false;
      cancelAnimationFrame(rafId);
    }

    frame(0); // one static frame immediately (also the reduced-motion end state)

    let io: IntersectionObserver | null = null;
    if (!reduce) {
      io = new IntersectionObserver(
        (es) => es.forEach((e) => (e.isIntersecting ? start() : stop())),
        { threshold: 0.01 },
      );
      io.observe(cv);
    }

    return () => {
      stop();
      ro.disconnect();
      io?.disconnect();
    };
  }, [still, galaxy]);

  return <canvas ref={canvasRef} className="mapcanvas" aria-hidden="true" />;
}

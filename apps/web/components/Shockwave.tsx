"use client";

import { useEffect, useRef } from "react";

interface ShockwaveProps {
  /**
   * Fires at the flash peak — the moment to mount the real star map behind the
   * white-out, so the heavy scene build is hidden under the flash.
   */
  onPeak: () => void;
  /** Fires when the warp is fully done and this overlay should unmount. */
  onComplete: () => void;
}

// Timeline (ms). The warp streaks accelerate outward, a shockwave ring bursts
// from centre, then a white flash peaks — under which we swap in the galaxy —
// before clearing to reveal the genesis star already igniting.
const T_FLASH_PEAK = 720;
const T_END = 1400;

/**
 * The teleport: a full-screen canvas that streaks the stars into hyperspace,
 * fires a shockwave ring, and white-flashes. It's mounted the instant analysis
 * completes and drives the handoff to the locked genesis ignition via onPeak.
 * Honors reduced motion by collapsing to a quick fade (peak then end, no RAF).
 */
export default function Shockwave({ onPeak, onComplete }: ShockwaveProps) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const flashRef = useRef<HTMLDivElement>(null);
  // keep the latest callbacks without restarting the animation
  const peakRef = useRef(onPeak);
  const endRef = useRef(onComplete);
  peakRef.current = onPeak;
  endRef.current = onComplete;

  useEffect(() => {
    const reduced =
      typeof matchMedia === "function" &&
      matchMedia("(prefers-reduced-motion:reduce)").matches;

    // Reduced motion: no streaks, just a brief white cross-fade with the same
    // peak/complete beats so the handoff logic is identical.
    if (reduced) {
      const flash = flashRef.current;
      if (flash) flash.style.opacity = "1";
      const p = setTimeout(() => peakRef.current(), 260);
      const e = setTimeout(() => endRef.current(), 620);
      return () => {
        clearTimeout(p);
        clearTimeout(e);
      };
    }

    const cv = canvasRef.current;
    const flash = flashRef.current;
    if (!cv || !flash) return;
    const ctx = cv.getContext("2d");
    if (!ctx) return;

    const DPR = Math.min(devicePixelRatio || 1, 2);
    let W = 0;
    let H = 0;
    const resize = () => {
      W = innerWidth;
      H = innerHeight;
      cv.width = W * DPR;
      cv.height = H * DPR;
      cv.style.width = W + "px";
      cv.style.height = H + "px";
      ctx.setTransform(DPR, 0, 0, DPR, 0, 0);
    };
    resize();
    addEventListener("resize", resize);

    // radial warp streaks emanating from centre
    const N = 220;
    const streaks = Array.from({ length: N }, () => ({
      ang: Math.random() * Math.PI * 2,
      r0: Math.random() * 40, // start near the centre
      speed: 0.6 + Math.random() * 1.6,
      len: 40 + Math.random() * 220,
      hue: Math.random() < 0.5 ? "220,235,255" : "255,240,210",
    }));

    const start = performance.now();
    let peaked = false;
    let ended = false;
    let rafId = 0;

    const frame = (now: number) => {
      const t = now - start;
      const k = Math.min(t / T_END, 1); // 0..1 overall
      const cx = W / 2;
      const cy = H / 2;

      ctx.clearRect(0, 0, W, H);
      ctx.globalCompositeOperation = "lighter";

      // ease-in acceleration: distance grows faster over time
      const accel = k * k;

      ctx.lineCap = "round";
      for (const s of streaks) {
        const dist = s.r0 + accel * s.speed * Math.hypot(W, H) * 0.9;
        const dx = Math.cos(s.ang);
        const dy = Math.sin(s.ang);
        const x1 = cx + dx * dist;
        const y1 = cy + dy * dist;
        const tail = s.len * (0.4 + accel * 2.2);
        const x2 = cx + dx * (dist - tail);
        const y2 = cy + dy * (dist - tail);
        const a = Math.min(0.9, 0.15 + accel * 1.1);
        const g = ctx.createLinearGradient(x2, y2, x1, y1);
        g.addColorStop(0, `rgba(${s.hue},0)`);
        g.addColorStop(1, `rgba(${s.hue},${a})`);
        ctx.strokeStyle = g;
        ctx.lineWidth = 1 + accel * 2;
        ctx.beginPath();
        ctx.moveTo(x2, y2);
        ctx.lineTo(x1, y1);
        ctx.stroke();
      }

      // expanding shockwave ring
      const ringR = accel * Math.hypot(W, H) * 0.85;
      const ringA = Math.max(0, 0.5 - k * 0.5);
      if (ringA > 0.01) {
        ctx.strokeStyle = `rgba(210,232,255,${ringA})`;
        ctx.lineWidth = 2 + accel * 6;
        ctx.beginPath();
        ctx.arc(cx, cy, ringR, 0, 6.28);
        ctx.stroke();
      }

      ctx.globalCompositeOperation = "source-over";

      // white flash: ramps up to the peak, then falls away to reveal the galaxy
      const peakK = T_FLASH_PEAK / T_END;
      const flashA =
        k < peakK
          ? Math.pow(k / peakK, 2)
          : Math.max(0, 1 - (k - peakK) / (1 - peakK));
      flash.style.opacity = String(flashA);

      if (!peaked && t >= T_FLASH_PEAK) {
        peaked = true;
        peakRef.current(); // mount the star map under the white-out
      }
      if (t >= T_END) {
        if (!ended) {
          ended = true;
          endRef.current();
        }
        return;
      }
      rafId = requestAnimationFrame(frame);
    };
    rafId = requestAnimationFrame(frame);

    return () => {
      cancelAnimationFrame(rafId);
      removeEventListener("resize", resize);
    };
  }, []);

  return (
    <div className="shockwave" aria-hidden="true">
      <canvas className="sw-canvas" ref={canvasRef} />
      <div className="sw-flash" ref={flashRef} />
    </div>
  );
}

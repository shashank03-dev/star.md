// Client-only, lazily-loaded animation stack for the landing page.
//
// Lenis + GSAP + ScrollTrigger are code-split out of the hero's critical path:
// nothing here is imported until a component actually calls loadAnim() in an
// effect, so first paint is never blocked by the animation libraries. GSAP and
// ScrollTrigger are registered exactly once and shared between the hero
// (which needs Lenis) and the legend (which needs ScrollTrigger timelines).
//
// One rAF drives everything: gsap.ticker runs Lenis, and Lenis's scroll event
// updates ScrollTrigger. The three runtimes never fight over the scroll frame.

import type Lenis from "lenis";
import type { gsap as GsapType } from "gsap";
import type { ScrollTrigger as ScrollTriggerType } from "gsap/ScrollTrigger";

export interface AnimStack {
  gsap: typeof GsapType;
  ScrollTrigger: typeof ScrollTriggerType;
  lenis: Lenis | null; // null when smooth scroll is intentionally disabled
  tickerFn: ((time: number) => void) | null; // the exact ticker callback, for teardown
}

let stackPromise: Promise<AnimStack> | null = null;

/** True when the visitor asked the OS to minimise motion. */
export function prefersReducedMotion(): boolean {
  return (
    typeof matchMedia === "function" &&
    matchMedia("(prefers-reduced-motion:reduce)").matches
  );
}

/**
 * Conservative low-end check. We only flag genuinely weak devices (so normal
 * laptops and mid-range phones keep the full effect); when flagged, the caller
 * skips the whole animation stack. Thresholds are intentionally low to avoid
 * over-degrading: Save-Data, <=2GB RAM, or <=2 logical cores.
 */
export function isWeakDevice(): boolean {
  if (typeof navigator === "undefined") return false;
  const n = navigator as Navigator & {
    deviceMemory?: number;
    connection?: { saveData?: boolean };
  };
  if (n.connection?.saveData) return true;
  if (typeof n.deviceMemory === "number" && n.deviceMemory <= 2) return true;
  if (typeof n.hardwareConcurrency === "number" && n.hardwareConcurrency <= 2)
    return true;
  return false;
}

/** Coarse pointer (touch) — Lenis is left off here; native scroll is smoother. */
export function isTouch(): boolean {
  return (
    typeof matchMedia === "function" && matchMedia("(pointer:coarse)").matches
  );
}

/**
 * The single source of truth for whether Lenis smooth scroll should run.
 * Both the hero and the legend call loadAnim(wantsSmooth()) so the cached
 * stack is created consistently regardless of which mounts first.
 */
export function wantsSmooth(): boolean {
  return !prefersReducedMotion() && !isWeakDevice() && !isTouch();
}

/**
 * Load and wire the animation stack once. `smooth` decides whether a Lenis
 * instance is created (false on touch / reduced-motion). GSAP + ScrollTrigger
 * are always returned so the legend can build its scroll timeline even when
 * smooth scrolling is off.
 */
export function loadAnim(smooth: boolean): Promise<AnimStack> {
  if (stackPromise) return stackPromise;

  stackPromise = (async () => {
    const [lenisMod, gsapMod, stMod] = await Promise.all([
      import("lenis"),
      import("gsap"),
      import("gsap/ScrollTrigger"),
    ]);

    const gsap = gsapMod.gsap ?? gsapMod.default;
    const ScrollTrigger = stMod.ScrollTrigger ?? stMod.default;
    gsap.registerPlugin(ScrollTrigger);

    let lenis: Lenis | null = null;
    let tickerFn: ((time: number) => void) | null = null;
    if (smooth) {
      const LenisCtor = lenisMod.default;
      lenis = new LenisCtor({ lerp: 0.1, wheelMultiplier: 1, smoothWheel: true });

      // Single loop: GSAP's ticker advances Lenis; Lenis's scroll updates ST.
      lenis.on("scroll", ScrollTrigger.update);
      tickerFn = (time: number) => lenis!.raf(time * 1000);
      gsap.ticker.add(tickerFn);
      gsap.ticker.lagSmoothing(0);

      // dev-only handle for manual/automated scroll verification; never in prod
      if (process.env.NODE_ENV !== "production") {
        (window as unknown as { __lenis?: Lenis }).__lenis = lenis;
      }
    }

    return { gsap, ScrollTrigger, lenis, tickerFn };
  })();

  return stackPromise;
}

/** Tear down Lenis + its ticker hook and reset the singleton (for HMR/unmount). */
export function disposeAnim() {
  if (!stackPromise) return;
  const p = stackPromise;
  stackPromise = null;
  p.then(({ gsap, lenis, tickerFn }) => {
    if (tickerFn) gsap.ticker.remove(tickerFn);
    if (lenis) lenis.destroy();
  });
}

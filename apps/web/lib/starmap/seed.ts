// Seeded RNG - identical math to the locked renderer. Every seed in the scene
// derives from the repo/branch names, so the same history always draws the same
// chart. Do not change these functions; the visual output depends on them.

/** FNV-1a string hash -> uint32. */
export const hashStr = (s: string): number => {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return h >>> 0;
};

/** Mulberry32 - deterministic uint32-seeded generator returning [0,1). */
export const rng = (s: number): (() => number) => {
  let a = s >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
};

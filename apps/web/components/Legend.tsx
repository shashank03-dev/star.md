"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { motion, type Variants } from "framer-motion";
import { loadAnim, prefersReducedMotion, isWeakDevice, wantsSmooth } from "@/lib/anim";
import { TextAnimate } from "@/registry/magicui/text-animate";
import StarMapPreview from "./StarMapPreview";
import type { StarMapPreviewApi } from "@/lib/starmap/renderer";

// The legend, rebuilt as a scroll-driven build of the *real* star map: one
// pinned galaxy that constructs itself as you scroll — genesis ignites, commits
// fill the arms, constellations draw, pull-requests flow home, releases ring up.
// As each beat arrives the caption blurs in per character (MagicUI TextAnimate),
// and a gradient rail (blue→amber) tracks progress. GSAP ScrollTrigger pins the
// stage and maps scroll → the galaxy's reveal and the active beat index. Under
// reduced-motion / weak devices / touch it degrades to a clean static list
// (`.static`) with no pinning.

interface Beat {
  key: string;
  short: string;
  term: string;
  celest: string;
  desc: string;
  glyph: React.ReactNode;
}

const BEATS: Beat[] = [
  {
    key: "genesis",
    short: "Initial commit",
    term: "Initial commit",
    celest: "the genesis star",
    desc: "Position zero, at the centre of it all. The largest, warmest star - it ignites alone, before any history exists to grow around it.",
    glyph: (
      <svg viewBox="0 0 72 72" fill="none" aria-hidden="true">
        <defs>
          <radialGradient id="lg-genesis" cx="50%" cy="50%" r="50%">
            <stop offset="0" stopColor="#F0C079" stopOpacity=".55" />
            <stop offset="55%" stopColor="#F0C079" stopOpacity=".10" />
            <stop offset="100%" stopColor="#F0C079" stopOpacity="0" />
          </radialGradient>
        </defs>
        <circle cx="36" cy="36" r="30" fill="url(#lg-genesis)" />
        <g stroke="#F5D9A6" strokeWidth="1.6" strokeLinecap="round">
          <line x1="36" y1="6" x2="36" y2="66" />
          <line x1="10" y1="21" x2="62" y2="51" />
          <line x1="10" y1="51" x2="62" y2="21" />
        </g>
        <circle cx="36" cy="36" r="5.5" fill="#FBEBCB" />
        <circle cx="36" cy="36" r="2.5" fill="#fff" />
      </svg>
    ),
  },
  {
    key: "commit",
    short: "Every commit",
    term: "Every commit",
    celest: "a star",
    desc: "Placed by its timestamp and sized in four magnitude tiers - the brightest earn diffraction spikes. Real points, chosen from your history, never invented.",
    glyph: (
      <svg viewBox="0 0 72 72" fill="none" aria-hidden="true">
        <g fill="#DCE7FF">
          <circle cx="18" cy="24" r="3.2" />
          <circle cx="52" cy="16" r="2.2" />
          <circle cx="40" cy="40" r="4.4" />
          <circle cx="60" cy="44" r="1.6" />
          <circle cx="24" cy="54" r="2.6" />
          <circle cx="50" cy="58" r="1.4" />
        </g>
        <g stroke="#DCE7FF" strokeWidth="1.2" strokeLinecap="round" opacity=".9">
          <line x1="40" y1="31" x2="40" y2="49" />
          <line x1="31" y1="40" x2="49" y2="40" />
        </g>
      </svg>
    ),
  },
  {
    key: "branch",
    short: "Each branch",
    term: "Each branch",
    celest: "a constellation",
    desc: "Its shape is drawn from the branch's own name, so no two look alike - and the same history always charts the same sky.",
    glyph: (
      <svg viewBox="0 0 72 72" fill="none" aria-hidden="true">
        <polyline
          points="10,50 24,30 40,38 54,18 64,34"
          fill="none"
          stroke="#6FA0FF"
          strokeWidth="1.4"
          strokeLinecap="round"
          strokeLinejoin="round"
          opacity=".8"
        />
        <g fill="#EAF1FF">
          <circle cx="10" cy="50" r="2.4" />
          <circle cx="24" cy="30" r="3.4" />
          <circle cx="40" cy="38" r="2" />
          <circle cx="54" cy="18" r="3" />
          <circle cx="64" cy="34" r="2.2" />
        </g>
      </svg>
    ),
  },
  {
    key: "pr",
    short: "Pull requests",
    term: "Pull requests",
    celest: "the lines home",
    desc: "Dotted merges trace each branch back to the trunk, with light flowing along every edge from parent commit to child.",
    glyph: (
      <svg viewBox="0 0 72 72" fill="none" aria-hidden="true">
        <line
          x1="14"
          y1="58"
          x2="14"
          y2="14"
          stroke="#3E7BFF"
          strokeWidth="2.4"
          strokeLinecap="round"
          opacity=".85"
        />
        <circle cx="56" cy="20" r="3.4" fill="#EAF1FF" />
        <circle cx="14" cy="40" r="3" fill="#EAF1FF" />
        <path
          d="M56 20 C 40 26, 30 32, 16 40"
          fill="none"
          stroke="#6FA0FF"
          strokeWidth="1.6"
          strokeLinecap="round"
          strokeDasharray="1 6"
        />
        <g fill="#BBD2FF">
          <circle cx="46" cy="24" r="1.5" />
          <circle cx="36" cy="29" r="1.5" />
          <circle cx="26" cy="34" r="1.5" />
        </g>
      </svg>
    ),
  },
  {
    key: "release",
    short: "Releases",
    term: "Releases",
    celest: "milestones",
    desc: "The bright, named stars - the moments your history was worth marking, ringed so they hold their place as the galaxy grows.",
    glyph: (
      <svg viewBox="0 0 72 72" fill="none" aria-hidden="true">
        <circle
          cx="36"
          cy="36"
          r="20"
          fill="none"
          stroke="#6FA0FF"
          strokeWidth="1.2"
          opacity=".45"
        />
        <g stroke="#DCE7FF" strokeWidth="1.5" strokeLinecap="round">
          <line x1="36" y1="20" x2="36" y2="52" />
          <line x1="20" y1="36" x2="52" y2="36" />
          <line x1="25" y1="25" x2="47" y2="47" />
          <line x1="25" y1="47" x2="47" y2="25" />
        </g>
        <circle cx="36" cy="36" r="4" fill="#fff" />
        <circle cx="56" cy="16" r="2" fill="#F0C079" />
      </svg>
    ),
  },
];

// Progress band edges (0..1) for each beat, and the rail-fraction each edge maps
// to. The rail fill + comet are driven through these so the leading edge travels
// node-to-node: it reaches tick i as beat i activates, instead of jumping.
const BEAT_P = [0, 0.14, 0.4, 0.62, 0.82, 1];
const TICK_F = [0, 0.25, 0.5, 0.75, 1, 1];

// which beat is active for a given scroll progress (0..1)
const beatAt = (p: number): number =>
  p < BEAT_P[1] ? 0 : p < BEAT_P[2] ? 1 : p < BEAT_P[3] ? 2 : p < BEAT_P[4] ? 3 : 4;

// remap raw scroll progress → rail fraction that passes exactly through the ticks
const railFraction = (p: number): number => {
  for (let i = 0; i < BEAT_P.length - 1; i++) {
    if (p <= BEAT_P[i + 1]) {
      const span = BEAT_P[i + 1] - BEAT_P[i] || 1;
      const t = (p - BEAT_P[i]) / span;
      return TICK_F[i] + (TICK_F[i + 1] - TICK_F[i]) * t;
    }
  }
  return 1;
};

// per-beat accent (cool → warm), used to tint the traveling comet
const BEAT_ACCENT = ["#4e86ff", "#6fa0ff", "#b7b4e0", "#e0be93", "#f0c079"];

// Static-list + heading reveal: fade/rise with a soft blur clearing.
const reveal: Variants = {
  hidden: { opacity: 0, y: 26, filter: "blur(10px)" },
  show: {
    opacity: 1,
    y: 0,
    filter: "blur(0px)",
    transition: { duration: 0.65, ease: [0.2, 0.7, 0.2, 1] },
  },
};

export default function Legend() {
  const rootRef = useRef<HTMLElement>(null);
  const buildRef = useRef<HTMLDivElement>(null);
  const stageRef = useRef<HTMLDivElement>(null);
  const railRef = useRef<HTMLDivElement>(null);
  const apiRef = useRef<StarMapPreviewApi | null>(null);

  const [live, setLive] = useState(false);
  const [active, setActive] = useState(0);

  // scroll drives this imperatively; React only re-renders on beat change
  const activeRef = useRef(0);

  // stable so StarMapPreview's effect never re-inits the renderer
  const onGalaxyReady = useCallback((api: StarMapPreviewApi) => {
    apiRef.current = api;
  }, []);

  useEffect(() => {
    const root = rootRef.current;
    const build = buildRef.current;
    if (!root || !build) return;

    // Static fallback: no WebGL, no pinning. Framer Motion whileInView reveals
    // the clean stacked list.
    if (prefersReducedMotion() || isWeakDevice() || !wantsSmooth()) {
      root.classList.add("static");
      return;
    }
    setLive(true); // mount the real Three.js galaxy (StarMapPreview)

    let dispose = () => {};
    let cancelled = false;

    loadAnim(true).then(({ gsap, ScrollTrigger }) => {
      if (cancelled) return;

      const gctx = gsap.context(() => {
        // Pin the stage and scrub the REAL renderer's growth choreography by
        // scroll: seek(p) drives genesis → present. The active beat index and
        // rail fill are derived from the same progress.
        ScrollTrigger.create({
          trigger: build,
          start: "top top",
          end: "+=520%",
          pin: true,
          scrub: 1,
          anticipatePin: 1,
          onUpdate: (self) => {
            const p = self.progress;
            apiRef.current?.seek(p);
            // rail fill + comet travel node-to-node; set straight on the DOM
            railRef.current?.style.setProperty("--rail", String(railFraction(p)));
            const i = beatAt(p);
            if (i !== activeRef.current) {
              activeRef.current = i;
              railRef.current?.style.setProperty("--comet", BEAT_ACCENT[i]);
              setActive(i);
            }
          },
        });
      }, root);

      dispose = () => gctx.revert();
    });

    return () => {
      cancelled = true;
      dispose();
    };
  }, []);

  const beat = BEATS[active];

  return (
    <section id="legend" className="legend" ref={rootRef}>
      <motion.div
        className="lg-head"
        variants={reveal}
        initial="hidden"
        whileInView="show"
        viewport={{ once: true, margin: "-10% 0px -10% 0px" }}
      >
        <div className="k">The legend</div>
        <h2>Everything in your history has a place in the sky.</h2>
        <p>
          The map isn&apos;t decoration. Every mark on it is a real point in your
          repository, read straight from public history - watch one build.
        </p>
      </motion.div>

      {/* desktop: the REAL star map builds itself as you scroll (its growth
          choreography scrubbed by scroll position) */}
      <div className="lg-build" ref={buildRef} aria-hidden="true">
        <div className="lg-stage" ref={stageRef}>
          {live && (
            <StarMapPreview
              repo="sindresorhus/slugify"
              autoplay={false}
              autoRotate={0.22}
              onReady={onGalaxyReady}
            />
          )}
          <div className="lg-scrim" />
          {/* each beat blurs in per character (MagicUI TextAnimate); keying by
              `active` re-triggers the reveal on every beat change. The parts are
              staggered by delay so the line reads left→right, then the desc. */}
          <div className="lg-captions" key={active}>
            <div className="lg-cap">
              <div className="lg-term">
                <TextAnimate
                  as="span"
                  by="character"
                  animation="blurInUp"
                  startOnView={false}
                  duration={0.5}
                >
                  {beat.term}
                </TextAnimate>
                <TextAnimate
                  as="span"
                  by="character"
                  animation="blurInUp"
                  startOnView={false}
                  delay={0.18}
                  duration={0.2}
                  className="arw"
                >
                  →
                </TextAnimate>
                <TextAnimate
                  as="span"
                  by="character"
                  animation="blurInUp"
                  startOnView={false}
                  delay={0.3}
                  duration={0.5}
                  className="celest"
                >
                  {beat.celest}
                </TextAnimate>
              </div>
              <TextAnimate
                as="p"
                by="word"
                animation="blurInUp"
                startOnView={false}
                delay={0.5}
                duration={0.6}
                className="lg-desc"
              >
                {beat.desc}
              </TextAnimate>
            </div>
          </div>
          <div className="lg-rail" ref={railRef}>
            <div className="lg-rail-track">
              <div className="lg-rail-fill" />
              <div className="lg-rail-comet" />
            </div>
            <div className="lg-rail-ticks">
              {BEATS.map((b, i) => (
                <div
                  className={`lg-tick${i <= active ? " on" : ""}${
                    i === active ? " current" : ""
                  }`}
                  key={b.key}
                >
                  <i />
                  <span>{b.short}</span>
                </div>
              ))}
            </div>
          </div>
        </div>
      </div>

      {/* fallback: clean static list (reduced-motion / weak / touch) */}
      <div className="lg-list">
        {BEATS.map((b) => (
          <motion.div
            className="lg-item"
            key={b.key}
            variants={reveal}
            initial="hidden"
            whileInView="show"
            viewport={{ once: true, margin: "-15% 0px -15% 0px" }}
          >
            <div className="lg-inline-glyph" aria-hidden="true">
              {b.glyph}
            </div>
            <div className="lg-term">
              {b.term} <span className="arw">→</span>{" "}
              <span className="celest">{b.celest}</span>
            </div>
            <p className="lg-desc">{b.desc}</p>
          </motion.div>
        ))}
      </div>

      <motion.div
        className="coda"
        variants={reveal}
        initial="hidden"
        whileInView="show"
        viewport={{ once: true, margin: "-10% 0px -10% 0px" }}
      >
        <p>
          Embed the map in your README and it re-charts itself on every push -{" "}
          <b>always current, always yours.</b>
        </p>
        <a href="#cta" className="btn btn-primary again">
          Chart your repository
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none">
            <path
              d="M5 12h14M13 6l6 6-6 6"
              stroke="#fff"
              strokeWidth="2"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
          </svg>
        </a>
      </motion.div>
    </section>
  );
}

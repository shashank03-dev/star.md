"use client";

import { useEffect, useRef, useState } from "react";
import StarSky from "./StarSky";
import MapPreview from "./MapPreview";
import StarMapPreview from "./StarMapPreview";
import Legend from "./Legend";
import RepoForm, { Spark, Arrow } from "./landing/RepoForm";
import Embed from "./landing/Embed";
import {
  loadAnim,
  prefersReducedMotion,
  isWeakDevice,
  wantsSmooth,
} from "@/lib/anim";
import { parseRepo } from "@/lib/parseRepo";
import { SOURCE_URL, isRepo } from "@/lib/site";
import "../app/landing.css";

const HEADLINE = ["Your", "repository", "is", "a", "galaxy."];
const SAMPLE = "sindresorhus/slugify"; // seeds the demo sky in the window
const LIVE = "shashank03-dev/star.md"; // a real repository for "open live map"

export default function Landing() {
  const [input, setInput] = useState("");
  const [still, setStill] = useState(false);
  // desktop gets the real Three.js star map in the window; touch and low-end
  // keep the cheap 2D canvas. Decided client-side (SSR renders the 2D one).
  const [use3D, setUse3D] = useState(false);
  const [leaving, setLeaving] = useState<{ x: number; y: number } | null>(null);

  const rootRef = useRef<HTMLDivElement>(null);
  const headerRef = useRef<HTMLElement>(null);
  const heroRef = useRef<HTMLElement>(null);
  const screenRef = useRef<HTMLDivElement>(null);
  const navRef = useRef<HTMLDivElement>(null);
  const hoverRef = useRef<HTMLSpanElement>(null);

  // Hero depth engine: a pinned, scroll-scrubbed cinematic (GSAP ScrollTrigger).
  // The copy dissolves upward, the camera cranes and dollies into the star-map
  // window while the horizon sinks and blurs away and the sky pushes forward; a
  // glint crosses the glass as it levels out. Touch gets a cheap non-pinned
  // parallax; reduced motion and weak devices get the flat final state.
  useEffect(() => {
    const root = rootRef.current;
    const hero = heroRef.current;
    if (!root || !hero) return;

    const setScrolled = () =>
      headerRef.current?.classList.toggle("scrolled", scrollY > 16);
    addEventListener("scroll", setScrolled, { passive: true });
    setScrolled();

    const onShow = (e: PageTransitionEvent) => e.persisted && setLeaving(null);
    addEventListener("pageshow", onShow);

    if (prefersReducedMotion() || isWeakDevice()) {
      root.classList.add("still");
      setStill(true);
      return () => {
        removeEventListener("scroll", setScrolled);
        removeEventListener("pageshow", onShow);
      };
    }

    const smooth = wantsSmooth();
    setUse3D(smooth);
    let cancelled = false;
    let dispose = () => {};

    loadAnim(smooth).then(({ gsap }) => {
      if (cancelled) return;
      const ctx = gsap.context(() => {
        // x: 0 discards the CSS translateX(-50%) GSAP would otherwise read as
        // pixels and stack on top of xPercent
        gsap.set(".window", {
          x: 0,
          xPercent: -50,
          z: -150,
          rotationX: 14,
          y: 24,
          scale: 0.92,
          transformOrigin: "50% 62%",
        });
        gsap.set(".horizon-far", { x: 0, xPercent: -50, z: -300 });
        gsap.set(".horizon-near", { x: 0, xPercent: -50, z: 20 });

        if (smooth) {
          const tl = gsap.timeline({
            defaults: { ease: "none" },
            scrollTrigger: {
              trigger: hero,
              start: "top top",
              end: "+=190%",
              scrub: 1,
              pin: true,
              anticipatePin: 1,
              invalidateOnRefresh: true,
              // pinned above the legend: refresh first so the legend's triggers
              // include this pin's spacer
              refreshPriority: 1,
            },
          });
          tl.to(".hero-copy", { yPercent: -34, autoAlpha: 0, filter: "blur(8px)", ease: "sine.inOut", duration: 0.58 }, 0)
            .to(hero, { "--hero-glow": 0, ease: "sine.inOut", duration: 0.58 }, 0)
            .to(".showcase", { y: () => -innerHeight * 0.27, ease: "power1.inOut", duration: 0.75 }, 0)
            .to(".window", { rotationX: 0, y: () => -innerHeight * 0.08, scale: 1.24, ease: "power2.inOut", duration: 1.15 }, 0)
            .fromTo(".window .glint", { xPercent: -120 }, { xPercent: 220, ease: "power1.inOut", duration: 0.9 }, 0.2)
            .to(".window .bar", { autoAlpha: 1, duration: 0.3 }, 0.55)
            .to("#sky", { scale: 1.14, ease: "none", duration: 1.15 }, 0)
            .to(".horizon-near", { y: () => innerHeight * 0.58, scale: 1.22, autoAlpha: 0, filter: "blur(8px)", ease: "sine.inOut", duration: 0.95 }, 0.32)
            .to(".horizon-far", { y: () => innerHeight * 0.34, autoAlpha: 0, filter: "blur(10px)", ease: "sine.inOut", duration: 0.95 }, 0.3);
        } else {
          gsap
            .timeline({
              defaults: { ease: "none" },
              scrollTrigger: { trigger: hero, start: "top top", end: "bottom top", scrub: true, invalidateOnRefresh: true },
            })
            .to(".window", { rotationX: 0, y: 0, scale: 1, duration: 1 }, 0)
            .to(".horizon-near", { y: () => -innerHeight * 0.08, duration: 1 }, 0)
            .to(".horizon-far", { y: () => -innerHeight * 0.03, duration: 1 }, 0);
        }
      }, root);
      dispose = () => ctx.revert();
    });

    return () => {
      cancelled = true;
      removeEventListener("scroll", setScrolled);
      removeEventListener("pageshow", onShow);
      dispose();
    };
  }, []);

  // observer's reticle over the window: crosshair + live RA/Dec readout
  useEffect(() => {
    const el = screenRef.current;
    if (!el || !matchMedia("(hover:hover) and (pointer:fine)").matches) return;
    const ra = el.querySelector<HTMLElement>(".ra");
    const dec = el.querySelector<HTMLElement>(".dec");
    const pad = (n: number) => String(Math.floor(n)).padStart(2, "0");
    const move = (e: PointerEvent) => {
      const r = el.getBoundingClientRect();
      const x = Math.max(0, Math.min(0.9999, (e.clientX - r.left) / r.width));
      const y = Math.max(0, Math.min(1, (e.clientY - r.top) / r.height));
      el.style.setProperty("--rx", x * 100 + "%");
      el.style.setProperty("--ry", y * 100 + "%");
      const h = x * 24;
      const d = (0.5 - y) * 180;
      if (ra) ra.textContent = `${pad(h)}h ${pad((h % 1) * 60)}m`;
      if (dec) dec.textContent = `${d < 0 ? "−" : "+"}${pad(Math.abs(d))}° ${pad((Math.abs(d) % 1) * 60)}'`;
    };
    const enter = () => el.classList.add("aim");
    const leave = () => el.classList.remove("aim");
    el.addEventListener("pointermove", move);
    el.addEventListener("pointerenter", enter);
    el.addEventListener("pointerleave", leave);
    return () => {
      el.removeEventListener("pointermove", move);
      el.removeEventListener("pointerenter", enter);
      el.removeEventListener("pointerleave", leave);
    };
  }, []);

  // nav: a soft pill glides to whichever link is hovered
  const hoverLink = (e: React.PointerEvent<HTMLAnchorElement>) => {
    const pill = hoverRef.current;
    const nav = navRef.current;
    if (!pill || !nav) return;
    const a = e.currentTarget.getBoundingClientRect();
    const n = nav.getBoundingClientRect();
    pill.style.width = a.width + 24 + "px";
    pill.style.transform = `translateX(${a.left - n.left - 12}px)`;
    pill.style.opacity = "1";
  };
  const leaveLinks = () => {
    if (hoverRef.current) hoverRef.current.style.opacity = "0";
  };

  const focusField = (e: React.MouseEvent) => {
    e.preventDefault();
    const el = document.getElementById("repo-hero") as HTMLInputElement | null;
    if (still) {
      scrollTo(0, 0);
      el?.focus({ preventScroll: true });
      return;
    }
    loadAnim(wantsSmooth()).then(({ lenis }) => {
      if (lenis) lenis.scrollTo(0, { duration: 1.2 });
      else scrollTo({ top: 0, behavior: "smooth" });
      setTimeout(() => el?.focus({ preventScroll: true }), 900);
    });
  };

  // the page goes dark from the button outward, then hands over to /map,
  // which opens on the same night ground
  const chart = (repo: string, from: HTMLElement) => {
    const url = "/map/?repo=" + encodeURIComponent(repo);
    if (still) return window.location.assign(url);
    const r = from.getBoundingClientRect();
    setLeaving({ x: r.left + r.width / 2, y: r.top + r.height / 2 });
    setTimeout(() => window.location.assign(url), 700);
  };

  const parsed = parseRepo(input);
  const seed = isRepo(parsed) ? parsed : LIVE;

  return (
    <div className="landing" ref={rootRef}>
      <StarSky />
      <div id="wash" />

      <header ref={headerRef}>
        <nav>
          <a href="#" className="brand" onClick={focusField} aria-label="Stellar">
            <span className="ico">
              <Spark />
            </span>
            Stellar
          </a>
          <div className="links" ref={navRef} onPointerLeave={leaveLinks}>
            <span className="hover-pill" ref={hoverRef} aria-hidden="true" />
            <a href="#legend" onPointerEnter={hoverLink}>
              Legend
            </a>
            <a href="#embed" onPointerEnter={hoverLink}>
              README embed
            </a>
            <a href={SOURCE_URL} target="_blank" rel="noreferrer" onPointerEnter={hoverLink}>
              GitHub
              <svg viewBox="0 0 12 12" aria-hidden="true">
                <path d="M3.5 8.5l5-5M4.5 3.5h4v4" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round" />
              </svg>
            </a>
          </div>
          <a href="#" className="btn btn-glass navcta" onClick={focusField}>
            Chart a repo
          </a>
        </nav>
      </header>

      <main>
        <section id="hero" className="stage" ref={heroRef}>
          <div className="hero-copy">
            <span className="eyebrow anim">
              <Spark className="st" />
              A star map for every GitHub repository
            </span>
            <h1 aria-label={HEADLINE.join(" ")}>
              {HEADLINE.map((w, i) => [
                i === 2 && <br key={"br" + i} />,
                <span
                  key={i}
                  aria-hidden="true"
                  className={"w" + (i === HEADLINE.length - 1 ? " glow" : "")}
                  style={{ "--i": i } as React.CSSProperties}
                >
                  {w}
                </span>,
              ])}
            </h1>
            <p className="sub anim">
              Paste a repo. Stellar charts its real Git history in 3D: the first
              commit ignites at the center, and every branch, merge and release
              grows out from it.
            </p>
            <div id="cta" className="anim">
              <RepoForm id="repo-hero" value={input} onChange={setInput} onChart={chart} examples />
            </div>
          </div>

          <div className="showcase anim-show">
            <img className="horizon horizon-far" src="/horizon-far.webp" alt="" aria-hidden="true" draggable={false} />

            <div className="window">
              <div className="bar">
                <i />
                <i />
                <i />
                <span>sample history · star map</span>
                <a className="live" href={"/map/?repo=" + encodeURIComponent(LIVE)}>
                  <b className="dot" />
                  Open a live map
                  <Arrow />
                </a>
              </div>
              <div className="screen" ref={screenRef}>
                {use3D ? (
                  <StarMapPreview repo={SAMPLE} autoRotate={0.45} />
                ) : (
                  <MapPreview still={still} repo={SAMPLE} />
                )}
                <div className="reticle" aria-hidden="true">
                  <i className="rh" />
                  <i className="rv" />
                  <i className="ring" />
                  <span className="readout">
                    RA <span className="ra">12h 00m</span>
                    <br />
                    DEC <span className="dec">+00° 00'</span>
                  </span>
                </div>
              </div>
              <span className="glint" aria-hidden="true" />
            </div>

            <img className="horizon horizon-near" src="/horizon-near.webp" alt="" aria-hidden="true" draggable={false} />
          </div>
        </section>

        <Legend />
        <Embed seed={seed} />

        <section className="closer">
          <h2>
            See what your history <span className="glow">looks like.</span>
          </h2>
          <RepoForm id="repo-final" value={input} onChange={setInput} onChart={chart} />
        </section>
      </main>

      <footer>
        <a href="#" className="brand" onClick={focusField}>
          <span className="ico">
            <Spark />
          </span>
          Stellar
        </a>
        <div className="fmeta">
          <a href={SOURCE_URL} target="_blank" rel="noreferrer">
            Source
          </a>
          <a href={"/map/?repo=" + encodeURIComponent(LIVE)}>Live map</a>
          <a href="#embed">Embed</a>
        </div>
        <p className="credit">
          Type: PP Neue Machina, PP Neue Montreal and PP Supply Mono by{" "}
          <a href="https://pangrampangram.com" target="_blank" rel="noreferrer">
            Pangram Pangram
          </a>
          , used under their personal-use licence.
        </p>
      </footer>

      {leaving && (
        <div
          className="lightsout"
          aria-hidden="true"
          style={{ "--ox": leaving.x + "px", "--oy": leaving.y + "px" } as React.CSSProperties}
        >
          <Spark className="lo-star" />
        </div>
      )}
    </div>
  );
}

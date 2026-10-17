"use client";

import { useEffect, useRef, useState } from "react";
import StarSky from "./StarSky";
import MapPreview from "./MapPreview";
import StarMapPreview from "./StarMapPreview";
import Legend from "./Legend";
import {
  loadAnim,
  prefersReducedMotion,
  isWeakDevice,
  wantsSmooth,
} from "@/lib/anim";
import { parseRepo } from "@/lib/parseRepo";
import "../app/landing.css";

export default function Landing() {
  const [input, setInput] = useState("sindresorhus/slugify");
  const [repo, setRepo] = useState("sindresorhus/slugify");
  const [still, setStill] = useState(false);
  // desktop gets the real Three.js star map in the window; low-end keeps the
  // cheap 2D canvas. Decided client-side (SSR renders the 2D fallback first).
  const [use3D, setUse3D] = useState(false);

  const rootRef = useRef<HTMLDivElement>(null);
  const headerRef = useRef<HTMLElement>(null);
  const heroRef = useRef<HTMLElement>(null);

  // Hero depth engine — a pinned, scroll-scrubbed cinematic driven by GSAP
  // ScrollTrigger. GSAP owns the layer transforms (the `xPercent:-50` it sets
  // replaces the horizontal centering the CSS did with translateX(-50%)). The
  // sequence: the copy lifts away, the "camera" cranes up and dollies into the
  // star-map window while the horizon sinks below the frame and the star field
  // pushes forward — you ascend into the galaxy. On touch we run a cheaper,
  // non-pinned parallax scrub (no pin jank on mobile); under reduced-motion or
  // weak devices we render the flat final state and load nothing.
  useEffect(() => {
    const root = rootRef.current;
    const hero = heroRef.current;
    if (!root || !hero) return;

    const reduce = prefersReducedMotion();
    const weak = isWeakDevice();

    // header condensation runs in every tier (cheap, class toggle only)
    const setScrolled = () =>
      headerRef.current?.classList.toggle("scrolled", scrollY > 16);
    addEventListener("scroll", setScrolled, { passive: true });
    setScrolled();

    if (reduce || weak) {
      root.classList.add("still"); // CSS pins the flat final state
      setStill(true); // freeze the map-preview canvas too
      root.style.setProperty("--p", "1");
      return () => removeEventListener("scroll", setScrolled);
    }

    const smooth = wantsSmooth();
    setUse3D(smooth); // real WebGL map only on the smooth desktop tier
    let cancelled = false;
    let dispose = () => {};

    loadAnim(smooth).then(({ gsap }) => {
      if (cancelled) return;

      const ctx = gsap.context(() => {
        // Base 3D placement. GSAP now writes these transforms inline (overriding
        // the CSS var(--p) rules), so it must carry the -50% centering itself.
        gsap.set(".window", {
          xPercent: -50,
          z: -150,
          rotationX: 13,
          y: 20,
          scale: 0.93,
          transformOrigin: "50% 62%",
        });
        gsap.set(".horizon-far", { xPercent: -50, z: -300 });
        gsap.set(".horizon-near", { xPercent: -50, z: 20 });

        if (smooth) {
          // DESKTOP: the full pinned cinematic.
          const tl = gsap.timeline({
            defaults: { ease: "none" },
            scrollTrigger: {
              trigger: hero,
              start: "top top",
              end: "+=185%",
              scrub: 1,
              pin: true,
              anticipatePin: 1,
              invalidateOnRefresh: true,
              // this pin sits above the legend and changes its scroll position,
              // so it must refresh first (higher priority = earlier) or the
              // legend's triggers compute their start/end without the pin spacer
              refreshPriority: 1,
            },
          });
          tl
            // copy softly dissolves up and out (blur + gentle ease so it recedes
            // rather than lingering half-faded and pale); the bloom goes with it
            .to(".hero-copy", { yPercent: -34, autoAlpha: 0, filter: "blur(7px)", ease: "sine.inOut", duration: 0.58 }, 0)
            .to(hero, { "--hero-glow": 0, ease: "sine.inOut", duration: 0.58 }, 0)
            // crane up so the emerging window rises to centre
            .to(".showcase", { y: () => -innerHeight * 0.26, ease: "power1.inOut", duration: 0.75 }, 0)
            // window flattens out of its tilt and dollies in (grows toward you),
            // ending as a large ROUNDED product shot of the finished star map
            .to(".window", { rotationX: 0, y: () => -innerHeight * 0.08, scale: 1.24, ease: "power2.inOut", duration: 1.15 }, 0)
            // star field pushes forward
            .to("#sky", { scale: 1.16, ease: "none", duration: 1.15 }, 0)
            // horizon lingers a beat, then gently sinks AND blurs away into deep
            // space — a soft dissolve, not an abrupt fade
            .to(".horizon-near", { y: () => innerHeight * 0.58, scale: 1.22, autoAlpha: 0, filter: "blur(7px)", ease: "sine.inOut", duration: 0.95 }, 0.32)
            .to(".horizon-far", { y: () => innerHeight * 0.34, autoAlpha: 0, filter: "blur(9px)", ease: "sine.inOut", duration: 0.95 }, 0.3);
        } else {
          // TOUCH: cheap, non-pinned parallax scrub.
          gsap.timeline({
            defaults: { ease: "none" },
            scrollTrigger: {
              trigger: hero,
              start: "top top",
              end: "bottom top",
              scrub: true,
              invalidateOnRefresh: true,
            },
          })
            .to(".window", { rotationX: 0, y: 0, scale: 1.0, duration: 1 }, 0)
            .to(".horizon-near", { y: () => -innerHeight * 0.08, duration: 1 }, 0)
            .to(".horizon-far", { y: () => -innerHeight * 0.03, duration: 1 }, 0);
        }
      }, root);

      dispose = () => ctx.revert();
    });

    return () => {
      cancelled = true;
      removeEventListener("scroll", setScrolled);
      dispose();
    };
  }, []);

  const mapHref = "/map/?repo=" + encodeURIComponent(repo);

  const chart = (e: React.FormEvent) => {
    e.preventDefault();
    const r = parseRepo(input);
    if (!r) return;
    setRepo(r);
    // the real action: open the live WebGL map for this repo
    window.location.assign("/map/?repo=" + encodeURIComponent(r));
  };

  return (
    <div className="landing" ref={rootRef}>
      <StarSky />
      <div id="wash" />

      <header ref={headerRef}>
        <nav>
          <div className="brand">
            <span className="ico">
              <svg width="13" height="13" viewBox="0 0 24 24">
                <path
                  d="M12 0 L14 10 L24 12 L14 14 L12 24 L10 14 L0 12 L10 10 Z"
                  fill="#dbe6ff"
                />
              </svg>
            </span>
            Stellar
          </div>
          <div className="links">
            <a href="#legend">The legend</a>
            <a href="#">How it works</a>
            <a href="#">Docs</a>
            <a href="#">GitHub</a>
          </div>
          <a href="#cta" className="btn btn-glass navcta">
            Chart a repo
          </a>
        </nav>
      </header>

      <main>
        <section id="hero" className="stage" ref={heroRef}>
          <div className="hero-copy">
            <span className="eyebrow anim">
              <span className="st">✦</span> A star map for your repository
            </span>
            <h1 className="anim">
              Your repository<span className="serif">is a galaxy.</span>
            </h1>
            <p className="sub anim">
              Paste a repo and watch it ignite from the very first commit, then
              grow star by star, branch by branch, into a living map you can keep
              and embed.
            </p>

            <div id="cta" className="anim">
              <form className="drop" onSubmit={chart}>
                {!/github\.com/i.test(input) && (
                  <span className="proto">github.com/</span>
                )}
                <input
                  id="repo"
                  type="text"
                  spellCheck={false}
                  autoComplete="off"
                  placeholder="owner/repo or paste a github.com URL"
                  value={input}
                  onChange={(e) => setInput(e.target.value)}
                  aria-label="GitHub repository"
                />
                <button type="submit" className="btn btn-primary">
                  Chart it{" "}
                  <svg width="15" height="15" viewBox="0 0 24 24" fill="none">
                    <path
                      d="M5 12h14M13 6l6 6-6 6"
                      stroke="#fff"
                      strokeWidth="2"
                      strokeLinecap="round"
                      strokeLinejoin="round"
                    />
                  </svg>
                </button>
              </form>
            </div>
            <div className="meta anim">
              <span>
                Reads <b>public</b> history
              </span>
              <span>No sign-up</span>
              <span>
                Free to <b>embed</b>
              </span>
            </div>
          </div>

          {/* the 3D showcase: window set into a layered horizon. Far ridge sits
              behind the window; the near foreground ridge (alpha cutout) paints
              in front and occludes its lower edge. */}
          <div className="showcase anim-show">
            <img
              className="horizon horizon-far"
              src="/horizon-far.webp"
              alt=""
              aria-hidden="true"
              draggable={false}
            />

            <div className="window">
              <div className="bar">
                <i />
                <i />
                <i />
                <span>{repo} · star map</span>
                <a className="live" href={mapHref}>
                  Chart it live
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none">
                    <path
                      d="M5 12h14M13 6l6 6-6 6"
                      stroke="currentColor"
                      strokeWidth="2"
                      strokeLinecap="round"
                      strokeLinejoin="round"
                    />
                  </svg>
                </a>
              </div>
              <div className="screen">
                {use3D ? (
                  <StarMapPreview repo={repo} autoRotate={0.45} />
                ) : (
                  <MapPreview still={still} repo={repo} />
                )}
              </div>
            </div>

            <img
              className="horizon horizon-near"
              src="/horizon-near.webp"
              alt=""
              aria-hidden="true"
              draggable={false}
            />
          </div>
        </section>

        <Legend />
      </main>

      <footer>
        <div className="brand">
          <span className="ico">
            <svg width="13" height="13" viewBox="0 0 24 24">
              <path
                d="M12 0 L14 10 L24 12 L14 14 L12 24 L10 14 L0 12 L10 10 Z"
                fill="#dbe6ff"
              />
            </svg>
          </span>
          Stellar
        </div>
        <div className="fmeta">
          <span>
            Reads <b>public</b> history
          </span>
          <span>No sign-up</span>
          <span>
            Free to <b>embed</b>
          </span>
        </div>
      </footer>
    </div>
  );
}

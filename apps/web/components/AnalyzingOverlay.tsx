"use client";

import { useEffect, useRef } from "react";

/** One row in the telemetry checklist. */
export interface StageView {
  id: string;
  label: string;
  status: "pending" | "active" | "done";
  /** trailing note, e.g. "71 commits", "main", "cached" */
  detail?: string;
  /** sub-resources that tick off independently (the history fan-out) */
  subs?: { key: string; label: string; done: boolean; detail?: string }[];
}

interface AnalyzingOverlayProps {
  repo: string;
  stages: StageView[];
  /** 0..1 accretion of the proto-star (fraction of work complete) */
  progress: number;
  /** true while the warp is flinging this UI toward the viewer */
  warping?: boolean;
  /** static render for reduced-motion / weak devices (no canvas RAF) */
  reduced?: boolean;
  error?: { code: string; message: string } | null;
  onRetry?: () => void;
}

// The accreting proto-star: a warm core that brightens tier by tier as work
// completes, with matter spiralling inward. It's the genesis star gathering
// itself before the warp ignites it. Kept to a compact canvas so it's cheap.
function ProtoStar({
  progress,
  reduced,
}: {
  progress: number;
  reduced?: boolean;
}) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const progRef = useRef(progress);
  progRef.current = progress;

  useEffect(() => {
    const cv = canvasRef.current;
    if (!cv) return;
    const ctx = cv.getContext("2d");
    if (!ctx) return;

    const DPR = Math.min(devicePixelRatio || 1, 2);
    const SIZE = 260;
    cv.width = SIZE * DPR;
    cv.height = SIZE * DPR;
    // display size comes from .proto-star (260px, smaller on phones)
    ctx.setTransform(DPR, 0, 0, DPR, 0, 0);
    const cx = SIZE / 2;
    const cy = SIZE / 2;

    // Infalling motes, seeded once; each spirals toward the core and respawns.
    const N = 46;
    const motes = Array.from({ length: N }, () => ({
      a: Math.random() * Math.PI * 2,
      r: 40 + Math.random() * 78,
      spd: 0.004 + Math.random() * 0.01,
      fall: 0.12 + Math.random() * 0.3,
      tw: Math.random() * 6.28,
    }));

    const draw = (t: number) => {
      const p = progRef.current;
      ctx.clearRect(0, 0, SIZE, SIZE);
      ctx.globalCompositeOperation = "lighter";

      // warm halo — grows and warms with progress
      const haloR = 34 + p * 46;
      const halo = ctx.createRadialGradient(cx, cy, 0, cx, cy, haloR + 46);
      const warm = 0.14 + p * 0.55;
      halo.addColorStop(0, `rgba(255,238,201,${warm})`);
      halo.addColorStop(0.4, `rgba(255,196,128,${warm * 0.5})`);
      halo.addColorStop(1, "rgba(255,150,90,0)");
      ctx.fillStyle = halo;
      ctx.beginPath();
      ctx.arc(cx, cy, haloR + 46, 0, 6.28);
      ctx.fill();

      // infalling motes (skipped when reduced: just the static core)
      if (!reduced) {
        for (const m of motes) {
          m.a += m.spd;
          m.r -= m.fall;
          if (m.r < 14) {
            m.r = 90 + Math.random() * 30;
            m.a = Math.random() * Math.PI * 2;
          }
          const x = cx + Math.cos(m.a) * m.r;
          const y = cy + Math.sin(m.a) * m.r * 0.62; // flattened accretion disc
          const near = 1 - (m.r - 14) / 106;
          const a = (0.25 + 0.6 * near) * (0.4 + p * 0.6);
          const tw = 0.6 + 0.4 * Math.sin(t * 0.004 + m.tw);
          ctx.fillStyle = `rgba(255,224,180,${a * tw})`;
          ctx.beginPath();
          ctx.arc(x, y, 1.1 + near * 1.3, 0, 6.28);
          ctx.fill();
        }
      }

      // the core itself — a tight bright center, brightening with progress
      const coreR = 7 + p * 9;
      const core = ctx.createRadialGradient(cx, cy, 0, cx, cy, coreR + 8);
      core.addColorStop(0, `rgba(255,252,244,${0.85 + p * 0.15})`);
      core.addColorStop(0.5, `rgba(255,231,190,${0.6 + p * 0.3})`);
      core.addColorStop(1, "rgba(255,190,120,0)");
      ctx.fillStyle = core;
      ctx.beginPath();
      ctx.arc(cx, cy, coreR + 8, 0, 6.28);
      ctx.fill();

      // a hint of diffraction spikes once the star is nearly formed
      if (p > 0.6) {
        const s = (p - 0.6) / 0.4;
        ctx.strokeStyle = `rgba(255,244,222,${0.35 * s})`;
        ctx.lineWidth = 1;
        const len = 22 + s * 30;
        ctx.beginPath();
        ctx.moveTo(cx - len, cy);
        ctx.lineTo(cx + len, cy);
        ctx.moveTo(cx, cy - len);
        ctx.lineTo(cx, cy + len);
        ctx.stroke();
      }

      ctx.globalCompositeOperation = "source-over";
      if (!reduced) rafId = requestAnimationFrame(draw);
    };

    let rafId = requestAnimationFrame(draw);
    if (reduced) {
      cancelAnimationFrame(rafId);
      draw(0); // single static frame reflecting current progress
    }
    return () => cancelAnimationFrame(rafId);
  }, [reduced]);

  return <canvas className="proto-star" ref={canvasRef} aria-hidden="true" />;
}

/**
 * The space-themed waiting page shown while /map streams the repo's history.
 * Purely presentational: StarMap owns the stream and feeds it `stages` +
 * `progress`; on success it flips `warping` on and the shockwave takes over.
 */
export default function AnalyzingOverlay({
  repo,
  stages,
  progress,
  warping,
  reduced,
  error,
  onRetry,
}: AnalyzingOverlayProps) {
  const cls = ["analyzing"];
  if (warping) cls.push("warping");
  if (error) cls.push("errored");

  return (
    <div className={cls.join(" ")} role="status" aria-live="polite">
      <div className="an-frame" aria-hidden="true">
        <span className="an-cnr tl" />
        <span className="an-cnr tr" />
        <span className="an-cnr bl" />
        <span className="an-cnr br" />
      </div>

      <div className="an-inner">
        {!error && <ProtoStar progress={progress} reduced={reduced} />}

        <div className="an-head">
          <div className="an-eyebrow">
            {error ? "Charting failed" : "Charting the heavens"}
          </div>
          <div className="an-repo">{repo}</div>
        </div>

        {error ? (
          <div className="an-error">
            <p className="an-error-msg">{humanError(error)}</p>
            {onRetry && (
              <button className="an-retry" onClick={onRetry}>
                Try again
              </button>
            )}
          </div>
        ) : (
          <ol className="an-tasks">
            {stages.map((s) => (
              <li key={s.id} className={"an-task " + s.status}>
                <span className="an-tick" aria-hidden="true">
                  {s.status === "done" ? "✦" : s.status === "active" ? "◆" : "◇"}
                </span>
                <span className="an-label">{s.label}</span>
                {s.detail && <span className="an-detail">{s.detail}</span>}
                {s.subs && s.status !== "pending" && (
                  <ul className="an-subs">
                    {s.subs.map((sub) => (
                      <li
                        key={sub.key}
                        className={"an-sub " + (sub.done ? "done" : "pending")}
                      >
                        <span className="an-sub-tick" aria-hidden="true">
                          {sub.done ? "·" : "∙"}
                        </span>
                        {sub.label}
                        {sub.detail && (
                          <span className="an-sub-detail">{sub.detail}</span>
                        )}
                      </li>
                    ))}
                  </ul>
                )}
              </li>
            ))}
          </ol>
        )}
      </div>
    </div>
  );
}

// Map the backend's machine codes to a calm, on-theme sentence.
function humanError(e: { code: string; message: string }): string {
  switch (e.code) {
    case "not_found":
      return "No such repository, or it isn't public.";
    case "empty_repo":
      return "This repository has no commits yet — nothing to chart.";
    case "rate_limited":
      return "GitHub's rate limit was reached. Try again in a little while.";
    case "too_many_requests":
      return "You're charting faster than the observatory allows. Wait a minute, then try again.";
    case "repo_too_large":
      return "This repository's history is too large to chart right now.";
    case "busy":
      return "Every telescope is busy right now. Try again in a few seconds.";
    case "bad_repo":
      return "That doesn't look like a repository. Use owner/name or a github.com URL.";
    default:
      return e.message || "Something went wrong while reading this repository.";
  }
}

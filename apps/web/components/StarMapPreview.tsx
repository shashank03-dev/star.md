"use client";

import { useEffect, useRef } from "react";
import { initStarMap, type StarMapPreviewApi } from "@/lib/starmap/renderer";
import { demoModel } from "@/lib/starmap/demoModel";

// Embeds the REAL Three.js star-map renderer (lib/starmap/renderer.ts) on the
// landing page, in preview mode: a synthesized demo history (no backend), sized
// to its container, no user controls/HUD, and either autoplaying its growth
// (hero) or externally scrubbed by scroll (legend, via onReady().seek). The
// renderer pauses itself while offscreen, so two of these on one page only draw
// when visible.
export default function StarMapPreview({
  repo = "sindresorhus/slugify",
  autoplay = true,
  autoRotate = 0.5,
  labels = false,
  className,
  onReady,
}: {
  repo?: string;
  autoplay?: boolean;
  autoRotate?: number;
  labels?: boolean;
  className?: string;
  onReady?: (api: StarMapPreviewApi) => void;
}) {
  const wrapRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!wrapRef.current) return;
    return initStarMap(wrapRef.current, repo, {
      model: demoModel(repo),
      interactive: false,
      sizeToContainer: true,
      hud: false,
      labels,
      autoplay,
      autoRotate,
      onReady,
    });
  }, [repo, autoplay, autoRotate, labels, onReady]);

  return (
    <div
      ref={wrapRef}
      className={"starpreview" + (className ? " " + className : "")}
      aria-hidden="true"
    />
  );
}

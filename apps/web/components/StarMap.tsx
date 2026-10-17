"use client";

import { useEffect, useRef, useState } from "react";
import { useSearchParams } from "next/navigation";
import type { Contract } from "@stellar/contract";
import { initStarMap } from "@/lib/starmap/renderer";
import { streamStarmap, StreamError, type ProgressEvent } from "@/lib/starmap/stream";
import { parseRepo } from "@/lib/parseRepo";
import { prefersReducedMotion, isWeakDevice } from "@/lib/anim";
import AnalyzingOverlay, { type StageView } from "./AnalyzingOverlay";
import Shockwave from "./Shockwave";
import "../app/map/starmap.css";
import "../app/map/analyzing.css";

// What the stream has reported so far. StarMap derives the checklist + the
// proto-star accretion (0..1) from this; the renderer never sees it.
interface Prog {
  resolveDone: boolean;
  defaultBranch?: string;
  headDone: boolean;
  commitTotal?: number;
  cached?: boolean;
  historyStarted: boolean;
  historyDone: boolean;
  historySkipped: boolean;
  history: Partial<Record<"commits" | "branches" | "tags" | "pulls", number>>;
  mapDone: boolean;
  stars?: number;
  constellations?: number;
}

const EMPTY: Prog = {
  resolveDone: false,
  headDone: false,
  historyStarted: false,
  historyDone: false,
  historySkipped: false,
  history: {},
  mapDone: false,
};

type Phase = "analyzing" | "warping" | "map";

const nf = new Intl.NumberFormat("en-US");

export default function StarMap() {
  const wrapRef = useRef<HTMLDivElement>(null);
  const modelRef = useRef<Contract | null>(null);
  const repo = parseRepo(useSearchParams().get("repo"));

  const [prog, setProg] = useState<Prog>(EMPTY);
  const [phase, setPhase] = useState<Phase>("analyzing");
  const [mountMap, setMountMap] = useState(false);
  const [error, setError] = useState<{ code: string; message: string } | null>(null);
  const [reduced, setReduced] = useState(false);
  const [attempt, setAttempt] = useState(0);

  // Stream the repo's history. On success we either warp (default) or cut
  // straight to the map (reduced motion / weak device). On failure the overlay
  // shows a themed error. Re-runs when `attempt` bumps (retry).
  useEffect(() => {
    if (!repo) {
      setError({ code: "bad_repo", message: "add ?repo=owner/name to the URL" });
      return;
    }
    const lowMotion = prefersReducedMotion() || isWeakDevice();
    setReduced(lowMotion);
    setProg(EMPTY);
    setError(null);
    setPhase("analyzing");
    setMountMap(false); // a re-analysis must not keep the previous repo's canvas

    const ac = new AbortController();
    const onEvent = (ev: ProgressEvent) =>
      setProg((prev) => {
        const next = { ...prev, history: { ...prev.history } };
        if (ev.phase === "resolve" && ev.status === "done") {
          next.resolveDone = true;
          next.defaultBranch = ev.defaultBranch;
        } else if (ev.phase === "head" && ev.status === "done") {
          next.headDone = true;
          next.commitTotal = ev.commitTotal;
          next.cached = ev.cached;
        } else if (ev.phase === "history") {
          if (ev.status === "start") next.historyStarted = true;
          else if (ev.status === "progress" && ev.resource)
            next.history[ev.resource] = ev.count ?? 0;
          else if (ev.status === "done") next.historyDone = true;
          else if (ev.status === "skip") {
            next.historyDone = true;
            next.historySkipped = true;
          }
        } else if (ev.phase === "map") {
          if (ev.status === "done") {
            next.mapDone = true;
            next.stars = ev.stars;
            next.constellations = ev.constellations;
          } else if (ev.status === "skip") {
            next.mapDone = true;
          }
        }
        return next;
      });

    streamStarmap(repo, onEvent, ac.signal)
      .then((model) => {
        if (ac.signal.aborted) return;
        modelRef.current = model;
        if (lowMotion) {
          setMountMap(true); // no warp — cut straight in
          setPhase("map");
        } else {
          setPhase("warping"); // Shockwave takes over from here
        }
      })
      .catch((err) => {
        if (ac.signal.aborted) return;
        const e =
          err instanceof StreamError
            ? { code: err.code, message: err.message }
            : { code: "network", message: String((err as Error)?.message || err) };
        setError(e);
      });

    return () => ac.abort();
  }, [repo, attempt]);

  // Mount the locked renderer once the model is ready and we've decided to show
  // it (at the warp's flash peak, or immediately under reduced motion). Injecting
  // `model` makes initStarMap skip its own fetch — the contract came from the
  // stream — so the reference look is untouched.
  useEffect(() => {
    if (!mountMap || !wrapRef.current || !modelRef.current) return;
    return initStarMap(wrapRef.current, repo, { model: modelRef.current });
  }, [mountMap, repo]);

  const stages = buildStages(prog, phase);
  const progress = phase === "warping" ? 1 : computeProgress(prog);
  const showOverlay = error != null || phase !== "map";

  return (
    <div className="starmap-root">
      <div id="wrap" ref={wrapRef}>
        <div id="labels" />
        <div id="frame" />
        <div id="plate">
          <b>Fig. III — Genesis</b>Uranographia Repositoria
          <br />
          3D · nebula field
        </div>
        <div id="hint">Drag to orbit · scroll to zoom</div>
        <div id="hud">
          <button id="replay">Replay</button>
          <input type="range" id="scrub" min={0} max={1000} defaultValue={0} />
          <span id="readout">—</span>
        </div>
        {/* the renderer's own overlay stays present (it hides itself when a model
            is injected); our AnalyzingOverlay layers above it during analysis */}
        <div id="overlay">
          <div className="spinner" />
          <div className="glyph" id="ovGlyph">
            Charting the heavens
          </div>
          <div className="sub" id="ovSub">
            reading repository history
          </div>
        </div>
      </div>

      {showOverlay && (
        <AnalyzingOverlay
          repo={repo || "unknown repository"}
          stages={stages}
          progress={progress}
          warping={phase === "warping"}
          reduced={reduced}
          error={error}
          onRetry={() => setAttempt((a) => a + 1)}
        />
      )}

      {phase === "warping" && (
        <Shockwave
          onPeak={() => setMountMap(true)}
          onComplete={() => setPhase("map")}
        />
      )}
    </div>
  );
}

// 0..1 accretion of the proto-star from real milestones.
function computeProgress(p: Prog): number {
  let v = 0;
  if (p.resolveDone) v += 0.12;
  if (p.headDone) v += 0.18;
  if (p.historyDone) v += 0.4;
  else {
    const got = ["commits", "branches", "tags", "pulls"].filter(
      (k) => p.history[k as keyof typeof p.history] !== undefined,
    ).length;
    v += 0.4 * (got / 4);
  }
  if (p.mapDone) v += 0.3;
  return Math.min(v, 1);
}

// Turn the raw progress into the ordered checklist the overlay renders.
function buildStages(p: Prog, phase: Phase): StageView[] {
  const st = (done: boolean, prevDone: boolean): StageView["status"] =>
    done ? "done" : prevDone ? "active" : "pending";

  const resolve: StageView = {
    id: "resolve",
    label: "Locating repository",
    status: p.resolveDone ? "done" : "active",
    detail: p.defaultBranch ? "default · " + p.defaultBranch : undefined,
  };

  const head: StageView = {
    id: "head",
    label: "Reading HEAD · counting commits",
    status: st(p.headDone, p.resolveDone),
    detail: p.headDone
      ? (p.cached ? "cached · " : "") + nf.format(p.commitTotal ?? 0) + " commits"
      : undefined,
  };

  const subDetail = (k: keyof Prog["history"]) =>
    p.history[k] !== undefined ? nf.format(p.history[k]!) : undefined;
  const history: StageView = {
    id: "history",
    label: "Gathering history",
    status: p.historySkipped
      ? "done"
      : st(p.historyDone, p.headDone && p.historyStarted),
    detail: p.historySkipped ? "cached" : undefined,
    subs: p.historySkipped
      ? undefined
      : [
          { key: "commits", label: "commits", done: p.history.commits !== undefined, detail: subDetail("commits") },
          { key: "branches", label: "branches", done: p.history.branches !== undefined, detail: subDetail("branches") },
          { key: "tags", label: "tags", done: p.history.tags !== undefined, detail: subDetail("tags") },
          { key: "pulls", label: "pull requests", done: p.history.pulls !== undefined, detail: subDetail("pulls") },
        ],
  };

  const map: StageView = {
    id: "map",
    label: "Mapping constellations",
    status: st(p.mapDone, p.historyDone),
    detail:
      p.mapDone && p.stars !== undefined
        ? nf.format(p.stars) + " stars · " + nf.format(p.constellations ?? 0) + " constellations"
        : undefined,
  };

  const ignite: StageView = {
    id: "ignite",
    label: "Igniting genesis star",
    status: phase === "warping" ? "active" : "pending",
  };

  return [resolve, head, history, map, ignite];
}

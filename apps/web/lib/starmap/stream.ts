// Client reader for the backend's NDJSON progress stream (GET /api/starmap/stream).
//
// The Go server (apps/server/main.go) flushes one JSON object per line while it
// builds the contract: a run of `progress` frames, then a terminal `result`
// (carrying the contract) or `error` frame. This reads that stream, hands each
// progress frame to `onEvent`, and resolves with the Contract (or rejects with a
// typed StreamError). The renderer then gets the model injected directly via
// initStarMap(..., { model }), so it never re-fetches.

import type { Contract } from "@stellar/contract";

// Same-origin in production (Go serves both). Override for split dev via
// NEXT_PUBLIC_API_BASE, matching the renderer's convention.
const API_BASE = process.env.NEXT_PUBLIC_API_BASE ?? "";

/** One progress frame from the stream (mirrors Go's progressEvent, sans result). */
export interface ProgressEvent {
  phase: "resolve" | "head" | "history" | "map";
  status?: "start" | "done" | "progress" | "skip";
  defaultBranch?: string;
  commitTotal?: number;
  headSha?: string;
  cached?: boolean;
  resource?: "commits" | "branches" | "tags" | "pulls";
  count?: number;
  stars?: number;
  constellations?: number;
}

/** Raw shape of any single NDJSON frame, including the terminal ones. */
interface Frame extends Omit<ProgressEvent, "phase"> {
  phase: ProgressEvent["phase"] | "result" | "error";
  code?: string;
  message?: string;
  contract?: Contract;
}

/** Carries the backend's machine `code` so callers can theme known errors. */
export class StreamError extends Error {
  code: string;
  constructor(code: string, message: string) {
    super(message || code);
    this.name = "StreamError";
    this.code = code;
  }
}

/**
 * Open the progress stream for `repo` and drive it to completion.
 * `onEvent` fires for every progress frame; the promise resolves with the
 * contract on `result` or rejects with a StreamError on `error` / transport
 * failure. Pass an AbortSignal to cancel on unmount.
 */
export async function streamStarmap(
  repo: string,
  onEvent: (ev: ProgressEvent) => void,
  signal?: AbortSignal,
): Promise<Contract> {
  const res = await fetch(
    API_BASE + "/api/starmap/stream?repo=" + encodeURIComponent(repo),
    { signal },
  );
  if (!res.ok || !res.body) {
    throw new StreamError(String(res.status), "stream failed to open");
  }

  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buf = "";
  let contract: Contract | null = null;

  // Parse one complete NDJSON line into its effect. Returns true once a terminal
  // frame (result/error) has been handled so the read loop can stop.
  const handleLine = (line: string): boolean => {
    line = line.trim();
    if (!line) return false;
    let frame: Frame;
    try {
      frame = JSON.parse(line) as Frame;
    } catch {
      return false; // ignore a partial/garbled line rather than aborting
    }
    if (frame.phase === "error") {
      throw new StreamError(frame.code || "error", frame.message || "");
    }
    if (frame.phase === "result") {
      if (!frame.contract) throw new StreamError("no_contract", "empty result");
      contract = frame.contract;
      return true;
    }
    onEvent(frame as ProgressEvent);
    return false;
  };

  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (value) buf += decoder.decode(value, { stream: true });

      let nl: number;
      while ((nl = buf.indexOf("\n")) !== -1) {
        const line = buf.slice(0, nl);
        buf = buf.slice(nl + 1);
        if (handleLine(line)) return contract!;
      }
      if (done) break;
    }
    // stream ended without a terminal frame — flush any trailing partial line
    if (handleLine(buf)) return contract!;
    throw new StreamError("truncated", "stream ended before result");
  } finally {
    reader.cancel().catch(() => {});
  }
}

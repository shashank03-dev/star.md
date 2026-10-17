"use client";

import { useEffect, useRef, useState } from "react";
import { motion } from "framer-motion";
import { parseRepo } from "@/lib/parseRepo";
import { API_URL, SITE_URL, isRepo } from "@/lib/site";

// README embed: pick a repository and a format, copy one line of Markdown. The
// preview is the real image the API serves for that repository, framed as a
// README so it reads as the finished result rather than a sample.

type Format = "gif" | "svg";

const FORMATS: Array<{ id: Format; label: string; note: string }> = [
  { id: "gif", label: "Animated", note: "GIF · grows from the first commit to HEAD" },
  { id: "svg", label: "Static", note: "SVG · lightweight, sharp at any size" },
];

export default function Embed({ seed }: { seed: string }) {
  const [input, setInput] = useState(seed);
  const [format, setFormat] = useState<Format>("gif");
  const [copied, setCopied] = useState(false);
  const [img, setImg] = useState<"loading" | "ok" | "error">("loading");
  const [origin, setOrigin] = useState(SITE_URL);
  const copyTimer = useRef(0);

  // follow the hero field until the visitor edits this one
  const touched = useRef(false);
  useEffect(() => {
    if (!touched.current && isRepo(seed)) setInput(seed);
  }, [seed]);

  useEffect(() => {
    if (location.origin.startsWith("https://")) setOrigin(location.origin);
  }, []);

  const parsed = parseRepo(input);
  const repo = isRepo(parsed) ? parsed : "OWNER/REPO";
  const q = encodeURIComponent(repo).replace("%2F", "/");
  const src =
    format === "gif"
      ? `${API_URL}/api/badge.gif?repo=${q}`
      : `${API_URL}/api/badge?repo=${q}&size=medium`;
  const href = `${origin}/map/?repo=${q}`;
  const snippet = `[![${repo.split("/")[1]} galaxy](${src})](${href})`;

  // debounce the preview so typing doesn't fire a request per keystroke
  const [shown, setShown] = useState(src);
  useEffect(() => {
    if (repo === "OWNER/REPO") return;
    const t = setTimeout(() => {
      setImg("loading");
      setShown(src);
    }, 450);
    return () => clearTimeout(t);
  }, [src, repo]);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(snippet);
    } catch {
      const ta = document.createElement("textarea");
      ta.value = snippet;
      document.body.appendChild(ta);
      ta.select();
      document.execCommand("copy");
      ta.remove();
    }
    setCopied(true);
    clearTimeout(copyTimer.current);
    copyTimer.current = window.setTimeout(() => setCopied(false), 1800);
  };

  return (
    <section id="embed" className="section embed">
      <motion.div
        className="sec-head"
        initial={{ opacity: 0, y: 26, filter: "blur(10px)" }}
        whileInView={{ opacity: 1, y: 0, filter: "blur(0px)" }}
        viewport={{ once: true, margin: "-10% 0px" }}
        transition={{ duration: 0.7, ease: [0.2, 0.7, 0.2, 1] }}
      >
        <div className="k">README embed</div>
        <h2>
          Put the galaxy <span className="glow">in your README.</span>
        </h2>
        <p>One line of Markdown. GitHub shows the image; a click opens the interactive map.</p>
      </motion.div>

      <motion.div
        className="embed-grid"
        initial={{ opacity: 0, y: 40 }}
        whileInView={{ opacity: 1, y: 0 }}
        viewport={{ once: true, margin: "-10% 0px" }}
        transition={{ duration: 0.9, ease: [0.2, 0.7, 0.2, 1], delay: 0.1 }}
      >
        <div className="embed-controls">
          <label className="ctl">
            <span className="ctl-label">Repository</span>
            <input
              type="text"
              spellCheck={false}
              autoComplete="off"
              autoCapitalize="off"
              value={input}
              placeholder="owner/repository"
              onChange={(e) => {
                touched.current = true;
                setInput(e.target.value);
              }}
            />
          </label>

          <div className="ctl">
            <span className="ctl-label" id="fmt-label">
              Format
            </span>
            <div className="seg" role="radiogroup" aria-labelledby="fmt-label">
              {FORMATS.map((f) => (
                <button
                  key={f.id}
                  type="button"
                  role="radio"
                  aria-checked={format === f.id}
                  className={format === f.id ? "on" : ""}
                  onClick={() => setFormat(f.id)}
                >
                  {format === f.id && (
                    <motion.span layoutId="seg-pill" className="seg-pill" transition={{ type: "spring", stiffness: 500, damping: 38 }} />
                  )}
                  <span className="seg-text">{f.label}</span>
                </button>
              ))}
            </div>
            <span className="ctl-note">{FORMATS.find((f) => f.id === format)!.note}</span>
          </div>

          <div className="snippet">
            <code>{snippet}</code>
            <button
              type="button"
              className={"copy" + (copied ? " done" : "")}
              onClick={copy}
              aria-label={copied ? "Copied" : "Copy Markdown"}
            >
              <svg viewBox="0 0 24 24" aria-hidden="true">
                <rect className="c1" x="8" y="8" width="12" height="12" rx="2" />
                <path className="c2" d="M16 8V6a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h2" />
                <path className="ok" d="M5 12.5l4.5 4.5L19 7.5" />
              </svg>
              <span>{copied ? "Copied" : "Copy"}</span>
            </button>
          </div>

          <p className="notes">
            Public repositories only. Images refresh within about fifteen minutes of a push.
          </p>
        </div>

        <div className="readme">
          <div className="readme-bar">
            <svg viewBox="0 0 16 16" aria-hidden="true">
              <path d="M2 2.5A1.5 1.5 0 0 1 3.5 1h6.1L14 5.4v8.1a1.5 1.5 0 0 1-1.5 1.5h-9A1.5 1.5 0 0 1 2 13.5v-11Z" fill="none" stroke="currentColor" />
            </svg>
            README.md
          </div>
          <div className="readme-body">
            <div className="readme-title">{repo === "OWNER/REPO" ? "your-repository" : repo.split("/")[1]}</div>
            <div className="readme-line w80" />
            <div className="readme-line w60" />
            <a className={"badge " + img} href={href} target="_blank" rel="noreferrer">
              {repo !== "OWNER/REPO" && (
                // eslint-disable-next-line @next/next/no-img-element
                <img
                  key={shown}
                  src={shown}
                  alt={`Stellar galaxy for ${repo}`}
                  onLoad={() => setImg("ok")}
                  onError={() => setImg("error")}
                />
              )}
              <span className="badge-state">
                {img === "error" || repo === "OWNER/REPO"
                  ? "Preview unavailable. The snippet still works once the repository is public."
                  : "Charting…"}
              </span>
            </a>
            <div className="readme-line w90" />
            <div className="readme-line w70" />
          </div>
        </div>
      </motion.div>
    </section>
  );
}

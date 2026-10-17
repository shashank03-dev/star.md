"use client";

import { useRef, useState } from "react";
import { parseRepo } from "@/lib/parseRepo";
import { EXAMPLES, isRepo } from "@/lib/site";

/** Four-point star, the brand mark and the inline glyph used across the page. */
export function Spark({ className }: { className?: string }) {
  return (
    <svg className={className} viewBox="0 0 24 24" aria-hidden="true">
      <path d="M12 0 L13.6 10.4 L24 12 L13.6 13.6 L12 24 L10.4 13.6 L0 12 L10.4 10.4 Z" />
    </svg>
  );
}

export function Arrow() {
  return (
    <svg className="arrow" viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <path
        d="M4 12h15M13 6l6 6-6 6"
        stroke="currentColor"
        strokeWidth="1.8"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

/**
 * The repository field. Accepts `owner/repo` or any github.com link (normalised
 * by parseRepo). An invalid submit shakes the field and explains the format
 * instead of navigating; a valid one hands the repo and the button (the origin
 * of the page transition) to `onChart`.
 */
export default function RepoForm({
  id,
  value,
  onChange,
  onChart,
  examples = false,
}: {
  id: string;
  value: string;
  onChange: (v: string) => void;
  onChart: (repo: string, from: HTMLElement) => void;
  examples?: boolean;
}) {
  const [bad, setBad] = useState(false);
  const formRef = useRef<HTMLFormElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const btnRef = useRef<HTMLButtonElement>(null);
  const typing = useRef(0);

  const repo = parseRepo(value);
  const valid = isRepo(repo);

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!valid) {
      setBad(true);
      const f = formRef.current;
      f?.classList.remove("shake");
      void f?.offsetWidth; // restart the animation on repeated attempts
      f?.classList.add("shake");
      inputRef.current?.focus();
      return;
    }
    onChart(repo, btnRef.current ?? formRef.current!);
  };

  // example chips type themselves into the field, one character at a time
  const pick = (r: string) => {
    clearInterval(typing.current);
    setBad(false);
    let i = 0;
    onChange("");
    typing.current = window.setInterval(() => {
      i++;
      onChange(r.slice(0, i));
      if (i >= r.length) {
        clearInterval(typing.current);
        btnRef.current?.focus({ preventScroll: true });
      }
    }, 22);
  };

  return (
    <div className="repoform">
      <form
        ref={formRef}
        className={"field" + (valid ? " valid" : "")}
        onSubmit={submit}
        onAnimationEnd={() => formRef.current?.classList.remove("shake")}
        noValidate
      >
        <label htmlFor={id} className="sr-only">
          GitHub repository
        </label>
        {!/github\.com/i.test(value) && (
          <span className="proto" aria-hidden="true">
            github.com/
          </span>
        )}
        <input
          ref={inputRef}
          id={id}
          type="text"
          inputMode="url"
          spellCheck={false}
          autoComplete="off"
          autoCapitalize="off"
          placeholder="owner/repository"
          value={value}
          aria-invalid={bad && !valid}
          aria-describedby={bad && !valid ? id + "-hint" : undefined}
          onChange={(e) => {
            clearInterval(typing.current);
            setBad(false);
            onChange(e.target.value);
          }}
        />
        <button ref={btnRef} type="submit" className="btn btn-primary">
          <span>Chart it</span>
          <Arrow />
        </button>
      </form>
      {bad && !valid ? (
        <p className="hint bad" id={id + "-hint"} role="alert">
          Use <code>owner/repository</code> or paste a github.com link.
        </p>
      ) : (
        examples && (
          <p className="hint examples">
            <span>Try</span>
            {EXAMPLES.map((r) => (
              <button type="button" key={r} onClick={() => pick(r)}>
                {r}
              </button>
            ))}
          </p>
        )
      )}
    </div>
  );
}

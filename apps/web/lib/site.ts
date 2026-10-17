// Public origins used by the landing page. The README snippet must carry
// absolute URLs (it is pasted into GitHub), so it falls back to the production
// deployments when the build has no explicit API origin.
export const SITE_URL = "https://stellar-web-taupe.vercel.app";
export const API_URL =
  process.env.NEXT_PUBLIC_API_BASE ||
  "https://stellar-api-shashank03-devs-projects.vercel.app";
export const SOURCE_URL = "https://github.com/shashank03-dev/star.md";

/** Repositories small enough to chart quickly, offered as one-click examples. */
export const EXAMPLES = [
  "shashank03-dev/star.md",
  "sindresorhus/slugify",
  "chalk/chalk",
];

/** `owner/repo` with GitHub's allowed characters. */
export const isRepo = (s: string) => /^[\w.-]+\/[\w.-]+$/.test(s);

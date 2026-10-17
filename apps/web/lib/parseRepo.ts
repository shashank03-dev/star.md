// Turn whatever the user pasted into a bare `owner/repo`.
//
// Accepts a full GitHub URL (https://github.com/owner/repo, with or without
// .git, and with trailing paths like /tree/main), a bare owner/repo, or a
// leading/trailing-slash-sloppy version of either. Returns "" when nothing
// repo-shaped is present. Shared by the landing input and the /map route so both
// front doors treat a pasted link the same way. Lowercased: GitHub names are
// case-insensitive and the server canonicalizes the same way, so every
// spelling maps to one galaxy and one URL.
export function parseRepo(raw: string | null | undefined): string {
  if (!raw) return "";
  raw = raw.trim();
  const m = raw.match(/github\.com\/([^/]+\/[^/#?]+)/i);
  if (m) return m[1].replace(/\.git$/, "").toLowerCase();
  return raw.replace(/^\/+|\/+$/g, "").toLowerCase();
}

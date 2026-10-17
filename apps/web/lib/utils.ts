// Minimal className joiner. (No Tailwind in this project, so tailwind-merge
// isn't needed — a truthy-filter join is enough for merging class names.)
export function cn(
  ...classes: Array<string | number | false | null | undefined>
): string {
  return classes.filter(Boolean).join(" ");
}

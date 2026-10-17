import Landing from "@/components/Landing";

// The landing is a client component (canvas, parallax, form), but Next still
// pre-renders its HTML at export time, so the marketing content is
// crawlable/SEO-complete in the emitted index.html.
export default function Page() {
  return <Landing />;
}

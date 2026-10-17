import { Suspense } from "react";
import type { Metadata } from "next";
import StarMap from "@/components/StarMap";

export const metadata: Metadata = {
  title: "Stellar - star map",
};

// StarMap reads ?repo via useSearchParams, which requires a Suspense boundary
// in a statically exported app.
export default function MapPage() {
  return (
    <Suspense fallback={null}>
      <StarMap />
    </Suspense>
  );
}

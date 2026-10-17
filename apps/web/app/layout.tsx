import type { Metadata } from "next";
import { Inter, Instrument_Serif } from "next/font/google";
import "./globals.css";

// Self-hosted at build time (no external font request) - replaces the landing's
// Google Fonts <link>. Exposed as CSS variables the page styles consume.
// One type pairing for the whole site, landing and star map alike: Inter for all
// UI and instrument text, Instrument Serif (italic) for display names. Loaded
// webfonts, so the star chart renders identically on every OS.
const inter = Inter({
  subsets: ["latin"],
  weight: ["400", "500", "600", "700", "800", "900"],
  variable: "--font-inter",
  display: "swap",
});

const instrumentSerif = Instrument_Serif({
  subsets: ["latin"],
  weight: "400",
  style: ["normal", "italic"],
  variable: "--font-instrument-serif",
  display: "swap",
});

export const metadata: Metadata = {
  title: "Stellar - every repository is a galaxy",
  description:
    "Paste a GitHub repo and watch it ignite from the very first commit, then grow star by star, branch by branch, into a living 3D star map you can keep and embed.",
  openGraph: {
    title: "Stellar - every repository is a galaxy",
    description:
      "Paste a repo and watch its history grow into a living 3D star map.",
    type: "website",
  },
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html
      lang="en"
      className={`${inter.variable} ${instrumentSerif.variable}`}
    >
      <body>{children}</body>
    </html>
  );
}

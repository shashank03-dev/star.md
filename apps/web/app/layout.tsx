import type { Metadata } from "next";
import {
  Geist,
  Geist_Mono,
  Inter,
  Instrument_Serif,
  JetBrains_Mono,
  Mona_Sans,
} from "next/font/google";
import "./globals.css";

// Self-hosted at build time (no external font request) - replaces the landing's
// Google Fonts <link>. Exposed as CSS variables the page styles consume.
// Map route: Inter (UI sans), Instrument Serif (label serif) and JetBrains Mono
// (the cartouche instrument type - a loaded webfont so the star chart renders
// identically on every OS). Landing: Mona Sans (variable width, set expanded
// for display), Geist for reading and Geist Mono for labels.
const inter = Inter({
  subsets: ["latin"],
  weight: ["400", "500", "600", "700", "800", "900"],
  variable: "--font-inter",
  display: "swap",
});

const mona = Mona_Sans({
  subsets: ["latin"],
  weight: "variable",
  axes: ["wdth"],
  variable: "--font-mona",
  display: "swap",
});

const geist = Geist({
  subsets: ["latin"],
  weight: "variable",
  variable: "--font-geist",
  display: "swap",
});

const geistMono = Geist_Mono({
  subsets: ["latin"],
  weight: ["400", "500"],
  variable: "--font-geist-mono",
  display: "swap",
});

const instrumentSerif = Instrument_Serif({
  subsets: ["latin"],
  weight: "400",
  style: ["normal", "italic"],
  variable: "--font-instrument-serif",
  display: "swap",
});

const jetbrainsMono = JetBrains_Mono({
  subsets: ["latin"],
  weight: ["400", "500", "600"],
  variable: "--font-jetbrains-mono",
  display: "swap",
});

export const metadata: Metadata = {
  title: "Stellar — your repository as a galaxy",
  description:
    "Paste a GitHub repository and Stellar draws its entire Git history as a 3D galaxy, from the first commit at the center to the latest at the edge. Embed it in your README.",
  openGraph: {
    title: "Stellar — your repository as a galaxy",
    description:
      "Your repository's Git history, drawn as a 3D galaxy you can explore and embed.",
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
      className={`${inter.variable} ${mona.variable} ${geist.variable} ${geistMono.variable} ${instrumentSerif.variable} ${jetbrainsMono.variable}`}
    >
      <body>{children}</body>
    </html>
  );
}

import type { Metadata } from "next";
import {
  Inter,
  Instrument_Serif,
  JetBrains_Mono,
} from "next/font/google";
import localFont from "next/font/local";
import "./globals.css";

// Self-hosted at build time (no external font request) - replaces the landing's
// Google Fonts <link>. Exposed as CSS variables the page styles consume.
// Map route: Inter (UI sans), Instrument Serif (label serif) and JetBrains Mono
// (the cartouche instrument type - a loaded webfont so the star chart renders
// identically on every OS). Landing: PP Neue Machina (display), PP Neue
// Montreal (reading) and PP Supply Mono (labels) by Pangram Pangram, used under
// their free personal-use licence and credited in the footer; self-hosted from
// app/fonts as latin-subset woff2.
const inter = Inter({
  subsets: ["latin"],
  weight: ["400", "500", "600", "700", "800", "900"],
  variable: "--font-inter",
  display: "swap",
});

const machina = localFont({
  src: [
    { path: "./fonts/PPNeueMachina-InktrapRegular.woff2", weight: "400" },
    { path: "./fonts/PPNeueMachina-InktrapUltrabold.woff2", weight: "800" },
  ],
  variable: "--font-machina",
  display: "swap",
});

const montreal = localFont({
  src: [
    { path: "./fonts/neuemontreal-light.woff2", weight: "300" },
    { path: "./fonts/neuemontreal-regular.woff2", weight: "400" },
    { path: "./fonts/neuemontreal-medium.woff2", weight: "500" },
  ],
  variable: "--font-montreal",
  display: "swap",
});

const supply = localFont({
  src: [
    { path: "./fonts/Supply-Regular.woff2", weight: "400" },
    { path: "./fonts/Supply-Medium.woff2", weight: "500" },
  ],
  variable: "--font-supply",
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
      className={`${inter.variable} ${machina.variable} ${montreal.variable} ${supply.variable} ${instrumentSerif.variable} ${jetbrainsMono.variable}`}
    >
      <body>{children}</body>
    </html>
  );
}

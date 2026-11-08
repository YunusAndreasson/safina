// @ts-check
import { defineConfig, fontProviders } from "astro/config";

// Static, zero-framework site. Dark only. Targets current evergreen browsers.
export default defineConfig({
  site: "https://safina.run",
  output: "static",
  devToolbar: { enabled: false },
  trailingSlash: "never",
  build: { inlineStylesheets: "always" },
  image: { responsiveStyles: false },
  fonts: [
    {
      // GitHub's Mona Sans: weight and width axes, so the headline and the
      // wordmark can run expanded while body copy stays at normal width.
      provider: fontProviders.local(),
      name: "Mona Sans",
      cssVariable: "--font-sans",
      fallbacks: ["system-ui", "sans-serif"],
      options: {
        variants: [
          {
            src: ["./src/assets/fonts/mona-sans-var.woff2"],
            weight: "200 900",
            stretch: "75% 125%",
            style: "normal",
            display: "swap",
          },
        ],
      },
    },
    {
      provider: fontProviders.local(),
      name: "JetBrains Mono",
      cssVariable: "--font-mono",
      fallbacks: ["ui-monospace", "monospace"],
      options: {
        variants: [
          {
            src: ["./src/assets/fonts/jetbrains-mono-var.woff2"],
            weight: "100 800",
            style: "normal",
            display: "swap",
          },
        ],
      },
    },
    {
      provider: fontProviders.local(),
      name: "Reem Kufi",
      cssVariable: "--font-kufi",
      fallbacks: ["serif"],
      options: {
        variants: [
          {
            src: ["./src/assets/fonts/reem-kufi-safina.woff2"],
            weight: "400 700",
            style: "normal",
            display: "block",
          },
        ],
      },
    },
  ],
  vite: {
    // Evergreen browsers from mid-2025 on, with Firefox ESR 140 as the floor.
    // One list drives both Oxc (JS) and Lightning CSS (cssTarget defaults to
    // it). Every feature the site leans on, from relative colors and nesting
    // to range media queries and individual transforms, ships natively there;
    // scroll-driven animations and view transitions stay behind @supports or
    // feature checks.
    build: { target: ["chrome137", "edge137", "firefox140", "safari18.5", "ios18.5"] },
  },
});

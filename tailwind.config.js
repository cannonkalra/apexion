/** @type {import('tailwindcss').Config} */

// Theme-aware colors are defined as CSS custom properties (see input.css) so
// that light mode is a *variable swap*, not a per-element class rewrite. The
// `base-*` neutral surfaces and the subset of `slate-*` text shades actually
// used by the UI resolve to `rgb(var(--…) / <alpha-value>)`; their values are
// declared on `:root` (dark, the default) and overridden under
// `[data-theme="light"]`. Brand/accent hues are intentionally fixed — they read
// well on both themes.
const v = (name) => `rgb(var(--${name}) / <alpha-value>)`;

module.exports = {
  darkMode: ["selector", '[data-theme="dark"]'],
  content: [
    "./internal/ui/**/*.templ",
    "./internal/ui/**/*_templ.go",
    // The reusable data-viewer components live outside internal/ui; scan their
    // templates, generated Go, and the helper .go files that return class names.
    "./internal/dataviewer/**/*.templ",
    "./internal/dataviewer/**/*_templ.go",
    "./internal/dataviewer/components/*.go",
  ],
  theme: {
    extend: {
      colors: {
        // templui-kit inspired neutral surfaces — now theme variables.
        base: {
          950: v("base-950"),
          900: v("base-900"),
          850: v("base-850"),
          800: v("base-800"),
          700: v("base-700"),
          600: v("base-600"),
          500: v("base-500"),
        },
        // Only the slate shades the UI actually uses are themed; the rest fall
        // back to Tailwind's defaults (unused, so harmless).
        slate: {
          100: v("slate-100"),
          200: v("slate-200"),
          300: v("slate-300"),
          400: v("slate-400"),
          500: v("slate-500"),
          600: v("slate-600"),
        },
        brand: {
          50: "#eef6ff",
          100: "#d9ecff",
          300: "#7cc0ff",
          400: "#409cff",
          500: "#1a7fff",
          600: "#0b63e6",
          700: "#0a4fbf",
        },
        accent: {
          emerald: "#22c98a",
          amber: "#f5b445",
          rose: "#f2557b",
          violet: "#9b7bff",
        },
      },
      fontFamily: {
        sans: ["Inter", "ui-sans-serif", "system-ui", "sans-serif"],
        mono: ["JetBrains Mono", "ui-monospace", "SFMono-Regular", "monospace"],
      },
      boxShadow: {
        card: "0 1px 2px 0 rgba(0,0,0,0.3), 0 1px 3px 0 rgba(0,0,0,0.15)",
      },
    },
  },
  plugins: [],
};

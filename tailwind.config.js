/** @type {import('tailwindcss').Config} */
module.exports = {
  darkMode: "class",
  content: ["./internal/ui/**/*.templ", "./internal/ui/**/*_templ.go"],
  theme: {
    extend: {
      colors: {
        // templui-kit inspired neutral + accent palette.
        base: {
          950: "#0a0b0f",
          900: "#111318",
          850: "#161920",
          800: "#1c2029",
          700: "#272c38",
          600: "#3a4150",
          500: "#5b6472",
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

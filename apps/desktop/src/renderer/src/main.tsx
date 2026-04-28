import ReactDOM from "react-dom/client";
import App from "./App";
// Inter variable font covers all weights (100-900) in a single file.
// Geist Mono kept as-is for code blocks; CJK is handled by system font fallback
// (see globals.css --font-sans chain). Keep font stack in sync with apps/web/app/layout.tsx.
import "@fontsource-variable/inter";
// Editorial serif — matches web's next/font Source_Serif_4. Loaded app-wide so
// onboarding headings and any future editorial surface can use `font-serif`
// (see tokens.css @theme inline). Variable font = one file covers all weights.
import "@fontsource-variable/source-serif-4";
import "@fontsource-variable/source-serif-4/wght-italic.css";
import "@fontsource/geist-mono/400.css";
import "@fontsource/geist-mono/700.css";
import "./globals.css";

// Match apps/web's theme-reset-v3 cleanup: next-themes 0.4.6 with
// enableSystem can leave a stale .dark class on <html> when the OS is in
// dark mode, even after explicit theme=light is stored. Clear stale theme
// classes and force theme=light once per install. See apps/web/app/layout.tsx
// for the full explanation. Bump the version suffix to re-fire for everyone.
try {
  if (localStorage.getItem("theme-reset-v3") !== "1") {
    localStorage.setItem("theme", "light");
    localStorage.setItem("theme-reset-v3", "1");
    document.documentElement.classList.remove("dark", "light");
  }
} catch {
  // localStorage unavailable — non-fatal, next-themes still runs.
}

ReactDOM.createRoot(document.getElementById("root")!).render(<App />);

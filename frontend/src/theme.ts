export type Theme = "paper" | "charcoal";
export function readTheme(): Theme {
  try {
    return localStorage.getItem("sulcata-theme") === "charcoal"
      ? "charcoal"
      : "paper";
  } catch {
    return "paper";
  }
}
export function applyTheme(theme: Theme) {
  document.documentElement.dataset.theme = theme;
  document
    .querySelector('meta[name="theme-color"]')
    ?.setAttribute("content", theme === "paper" ? "#e9e3d5" : "#222120");
  try {
    localStorage.setItem("sulcata-theme", theme);
  } catch {
    /* Appearance remains usable without browser storage. */
  }
}

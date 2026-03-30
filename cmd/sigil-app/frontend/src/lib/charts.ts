// Shared chart utilities for Canvas 2D rendering.
// Zero external dependencies — all rendering is manual.

export const COLORS = {
  accent: "#0071e3",
  accentLight: "rgba(0, 113, 227, 0.15)",
  success: "#34c759",
  danger: "#ff3b30",
  warning: "#ff9f0a",
  fg: "#1d1d1f",
  fgSecondary: "#86868b",
  border: "#d2d2d7",
  bg: "#ffffff",
  bgSecondary: "#f5f5f7",
};

// Detect dark mode for canvas rendering.
export function isDark(): boolean {
  return (
    typeof window !== "undefined" &&
    window.matchMedia("(prefers-color-scheme: dark)").matches
  );
}

export function getColors() {
  if (isDark()) {
    return {
      ...COLORS,
      fg: "#f5f5f7",
      fgSecondary: "#98989d",
      border: "#38383a",
      bg: "#1c1c1e",
      bgSecondary: "#2c2c2e",
      accent: "#0a84ff",
      accentLight: "rgba(10, 132, 255, 0.15)",
      success: "#30d158",
      danger: "#ff453a",
      warning: "#ffd60a",
    };
  }
  return COLORS;
}

// Format axis labels.
export function formatDate(dateStr: string): string {
  const parts = dateStr.split("-");
  return `${parts[1]}/${parts[2]}`;
}

export function formatNumber(n: number): string {
  if (n >= 1000) return `${(n / 1000).toFixed(1)}k`;
  return String(n);
}

export function formatPercent(n: number): string {
  return `${Math.round(n * 100)}%`;
}

// Setup a canvas for HiDPI rendering.
export function setupCanvas(
  canvas: HTMLCanvasElement,
  width: number,
  height: number
): CanvasRenderingContext2D {
  const dpr = window.devicePixelRatio || 1;
  canvas.width = width * dpr;
  canvas.height = height * dpr;
  canvas.style.width = `${width}px`;
  canvas.style.height = `${height}px`;
  const ctx = canvas.getContext("2d")!;
  ctx.scale(dpr, dpr);
  return ctx;
}

// Draw grid lines.
export function drawGrid(
  ctx: CanvasRenderingContext2D,
  width: number,
  height: number,
  padding: { top: number; right: number; bottom: number; left: number },
  rows: number
) {
  const colors = getColors();
  ctx.strokeStyle = colors.border;
  ctx.lineWidth = 0.5;

  const chartH = height - padding.top - padding.bottom;

  for (let i = 0; i <= rows; i++) {
    const y = padding.top + (chartH / rows) * i;
    ctx.beginPath();
    ctx.moveTo(padding.left, y);
    ctx.lineTo(width - padding.right, y);
    ctx.stroke();
  }
}

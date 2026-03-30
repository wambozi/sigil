import { useEffect, useRef } from "preact/hooks";
import { setupCanvas, getColors, formatPercent } from "../lib/charts";

interface CategoryData {
  category: string;
  count: number;
  acceptance_rate: number;
}

export function CategoryChart({ data }: { data: CategoryData[] }) {
  const canvasRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas || data.length === 0) return;

    const sorted = [...data].sort((a, b) => b.count - a.count).slice(0, 8);
    const w = canvas.parentElement?.clientWidth || 360;
    const rowH = 28;
    const h = Math.max(sorted.length * rowH + 24, 60);
    const pad = { left: 100, right: 50 };
    const ctx = setupCanvas(canvas, w, h);
    const colors = getColors();

    ctx.clearRect(0, 0, w, h);

    const maxCount = Math.max(...sorted.map((d) => d.count), 1);
    const barMaxW = w - pad.left - pad.right;

    sorted.forEach((d, i) => {
      const y = i * rowH + 12;
      const barW = (d.count / maxCount) * barMaxW;

      // Category label.
      ctx.fillStyle = colors.fgSecondary;
      ctx.font = "11px -apple-system, sans-serif";
      ctx.textAlign = "right";
      ctx.fillText(d.category, pad.left - 8, y + 14);

      // Bar.
      ctx.fillStyle = colors.accent;
      ctx.beginPath();
      ctx.roundRect(pad.left, y + 4, barW, 16, 3);
      ctx.fill();

      // Count + acceptance rate.
      ctx.fillStyle = colors.fg;
      ctx.textAlign = "left";
      ctx.font = "10px -apple-system, sans-serif";
      ctx.fillText(
        `${d.count} (${formatPercent(d.acceptance_rate)})`,
        pad.left + barW + 6,
        y + 15
      );
    });
  }, [data]);

  return (
    <div class="chart-container">
      <div class="chart-title">Categories</div>
      <canvas ref={canvasRef} />
    </div>
  );
}

import { useEffect, useRef } from "preact/hooks";
import { setupCanvas, drawGrid, getColors, formatDate, formatNumber } from "../lib/charts";

interface DailyCount {
  date: string;
  total: number;
  accepted: number;
  dismissed: number;
  pending: number;
}

export function DailyChart({ data }: { data: DailyCount[] }) {
  const canvasRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas || data.length === 0) return;

    const w = canvas.parentElement?.clientWidth || 360;
    const h = 180;
    const pad = { top: 20, right: 16, bottom: 32, left: 44 };
    const ctx = setupCanvas(canvas, w, h);
    const colors = getColors();

    ctx.clearRect(0, 0, w, h);

    const maxVal = Math.max(...data.map((d) => d.total), 1);
    const chartW = w - pad.left - pad.right;
    const chartH = h - pad.top - pad.bottom;
    const barW = Math.max(chartW / data.length - 2, 2);

    drawGrid(ctx, w, h, pad, 4);

    // Draw bars.
    data.forEach((d, i) => {
      const x = pad.left + (chartW / data.length) * i + 1;
      const barH = (d.total / maxVal) * chartH;
      const y = pad.top + chartH - barH;

      ctx.fillStyle = colors.accent;
      ctx.fillRect(x, y, barW, barH);
    });

    // Y-axis labels.
    ctx.fillStyle = colors.fgSecondary;
    ctx.font = "10px -apple-system, sans-serif";
    ctx.textAlign = "right";
    for (let i = 0; i <= 4; i++) {
      const y = pad.top + (chartH / 4) * i;
      const val = maxVal * (1 - i / 4);
      ctx.fillText(formatNumber(Math.round(val)), pad.left - 6, y + 4);
    }

    // X-axis labels.
    ctx.textAlign = "center";
    const interval = Math.max(Math.floor(data.length / 5), 1);
    data.forEach((d, i) => {
      if (i % interval === 0 || i === data.length - 1) {
        const x = pad.left + (chartW / data.length) * i + barW / 2;
        ctx.fillText(formatDate(d.date), x, h - 8);
      }
    });
  }, [data]);

  return (
    <div class="chart-container">
      <div class="chart-title">Daily Suggestions</div>
      <canvas ref={canvasRef} />
    </div>
  );
}

import { useEffect, useRef } from "preact/hooks";
import { setupCanvas, getColors } from "../lib/charts";

export function HeatmapChart({ data }: { data: number[] }) {
  const canvasRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas || data.length !== 24) return;

    const w = canvas.parentElement?.clientWidth || 360;
    const h = 60;
    const pad = { left: 4, right: 4, top: 20, bottom: 16 };
    const ctx = setupCanvas(canvas, w, h);
    const colors = getColors();

    ctx.clearRect(0, 0, w, h);

    const maxVal = Math.max(...data, 1);
    const cellW = (w - pad.left - pad.right) / 24;
    const cellH = h - pad.top - pad.bottom;

    data.forEach((count, i) => {
      const x = pad.left + i * cellW;
      const intensity = count / maxVal;

      // Interpolate between bg and accent.
      ctx.fillStyle =
        intensity === 0
          ? colors.bgSecondary
          : `rgba(${colors.accent === "#0a84ff" ? "10, 132, 255" : "0, 113, 227"}, ${0.15 + intensity * 0.85})`;

      ctx.beginPath();
      ctx.roundRect(x + 1, pad.top, cellW - 2, cellH, 3);
      ctx.fill();

      // Hour label every 3 hours.
      if (i % 3 === 0) {
        ctx.fillStyle = colors.fgSecondary;
        ctx.font = "9px -apple-system, sans-serif";
        ctx.textAlign = "center";
        ctx.fillText(`${i}`, x + cellW / 2, h - 2);
      }
    });

    // Title.
    ctx.fillStyle = colors.fgSecondary;
    ctx.font = "11px -apple-system, sans-serif";
    ctx.textAlign = "left";
    ctx.fillText("Hourly Distribution", pad.left, 12);
  }, [data]);

  return (
    <div class="chart-container chart-heatmap">
      <canvas ref={canvasRef} />
    </div>
  );
}

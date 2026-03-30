import { useEffect, useRef } from "preact/hooks";
import { setupCanvas, getColors } from "../lib/charts";

interface DailyHourly {
  date: string;
  hours: number[];
}

export function HeatmapChart({
  data,
  dailyHourly,
}: {
  data: number[];
  dailyHourly?: DailyHourly[];
}) {
  const canvasRef = useRef<HTMLCanvasElement>(null);

  // Use per-day data if available, otherwise fall back to aggregate row.
  const rows = dailyHourly && dailyHourly.length > 0 ? dailyHourly : null;

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;

    const colors = getColors();
    const w = canvas.parentElement?.clientWidth || 360;
    const rowCount = rows ? rows.length : 1;
    const cellH = 20;
    const gap = 2;
    const pad = { left: 52, right: 4, top: 8, bottom: 20 };
    const h = pad.top + rowCount * (cellH + gap) + pad.bottom;
    const ctx = setupCanvas(canvas, w, h);

    ctx.clearRect(0, 0, w, h);

    const cellW = (w - pad.left - pad.right) / 24;

    // Find global max for consistent color scaling.
    let maxVal = 1;
    if (rows) {
      for (const row of rows) {
        for (const v of row.hours) {
          if (v > maxVal) maxVal = v;
        }
      }
    } else {
      maxVal = Math.max(...data, 1);
    }

    const accentRGB =
      colors.accent === "#0a84ff" ? "10, 132, 255" : "0, 113, 227";

    const drawRow = (hours: number[], y: number) => {
      for (let i = 0; i < 24; i++) {
        const x = pad.left + i * cellW;
        const intensity = hours[i] / maxVal;

        ctx.fillStyle =
          intensity === 0
            ? colors.bgSecondary
            : `rgba(${accentRGB}, ${0.12 + intensity * 0.88})`;

        ctx.beginPath();
        ctx.roundRect(x + 1, y, cellW - 2, cellH, 3);
        ctx.fill();
      }
    };

    if (rows) {
      // Multi-row: one row per day.
      rows.forEach((row, ri) => {
        const y = pad.top + ri * (cellH + gap);

        // Day label.
        ctx.fillStyle = colors.fgSecondary;
        ctx.font = "10px -apple-system, sans-serif";
        ctx.textAlign = "right";
        const dayLabel = formatDayLabel(row.date);
        ctx.fillText(dayLabel, pad.left - 6, y + cellH / 2 + 3);

        drawRow(row.hours, y);
      });
    } else {
      // Single aggregate row.
      drawRow(data, pad.top);
    }

    // Hour labels along bottom.
    ctx.fillStyle = colors.fgSecondary;
    ctx.font = "9px -apple-system, sans-serif";
    ctx.textAlign = "center";
    const bottomY = pad.top + rowCount * (cellH + gap) + 10;
    for (let i = 0; i < 24; i += 3) {
      const x = pad.left + i * cellW + cellW / 2;
      ctx.fillText(`${i}`, x, bottomY);
    }
  }, [data, dailyHourly]);

  return (
    <div class="chart-container chart-heatmap">
      <div class="chart-title">Activity by Hour</div>
      <canvas ref={canvasRef} />
    </div>
  );
}

function formatDayLabel(dateStr: string): string {
  const d = new Date(dateStr + "T12:00:00");
  const today = new Date();
  today.setHours(12, 0, 0, 0);

  const diff = Math.round(
    (today.getTime() - d.getTime()) / (1000 * 60 * 60 * 24)
  );

  if (diff === 0) return "Today";
  if (diff === 1) return "Yesterday";

  return d.toLocaleDateString(undefined, { weekday: "short" });
}

import { useEffect, useRef } from "preact/hooks";
import { setupCanvas, drawGrid, getColors, formatDate, formatPercent } from "../lib/charts";

interface DailyCount {
  date: string;
  total: number;
  accepted: number;
  dismissed: number;
}

export function AcceptanceChart({ data }: { data: DailyCount[] }) {
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
    drawGrid(ctx, w, h, pad, 4);

    // Compute acceptance rate per day.
    const rates = data.map((d) => {
      const resolved = d.accepted + d.dismissed;
      return resolved > 0 ? d.accepted / resolved : 0;
    });

    const chartW = w - pad.left - pad.right;
    const chartH = h - pad.top - pad.bottom;
    const stepX = chartW / Math.max(rates.length - 1, 1);

    // Draw line.
    ctx.beginPath();
    ctx.strokeStyle = colors.accent;
    ctx.lineWidth = 2;
    ctx.lineJoin = "round";

    rates.forEach((rate, i) => {
      const x = pad.left + i * stepX;
      const y = pad.top + chartH * (1 - rate);
      if (i === 0) ctx.moveTo(x, y);
      else ctx.lineTo(x, y);
    });
    ctx.stroke();

    // Fill area.
    ctx.lineTo(pad.left + (rates.length - 1) * stepX, pad.top + chartH);
    ctx.lineTo(pad.left, pad.top + chartH);
    ctx.closePath();
    ctx.fillStyle = colors.accentLight;
    ctx.fill();

    // Y-axis labels.
    ctx.fillStyle = colors.fgSecondary;
    ctx.font = "10px -apple-system, sans-serif";
    ctx.textAlign = "right";
    for (let i = 0; i <= 4; i++) {
      const y = pad.top + (chartH / 4) * i;
      ctx.fillText(formatPercent(1 - i / 4), pad.left - 6, y + 4);
    }

    // X-axis labels (every ~7 days).
    ctx.textAlign = "center";
    const interval = Math.max(Math.floor(data.length / 5), 1);
    data.forEach((d, i) => {
      if (i % interval === 0 || i === data.length - 1) {
        const x = pad.left + i * stepX;
        ctx.fillText(formatDate(d.date), x, h - 8);
      }
    });
  }, [data]);

  return (
    <div class="chart-container">
      <div class="chart-title">Acceptance Rate</div>
      <canvas ref={canvasRef} />
    </div>
  );
}

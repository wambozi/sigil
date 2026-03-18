import { useEffect, useRef, useState } from "preact/hooks";
import { Chart, registerables } from "chart.js";
import { fetchQuality, type QualityData } from "../api";

Chart.register(...registerables);

export function QualityView() {
  const [data, setData] = useState<QualityData | null>(null);
  const [error, setError] = useState("");
  const chartRef = useRef<HTMLCanvasElement>(null);
  const chartInstance = useRef<Chart | null>(null);

  useEffect(() => {
    fetchQuality()
      .then(setData)
      .catch((e: Error) => setError(e.message));
  }, []);

  useEffect(() => {
    if (!data || !chartRef.current) return;

    chartInstance.current?.destroy();

    const dates = data.data.map((d) => d.date);

    chartInstance.current = new Chart(chartRef.current, {
      type: "line",
      data: {
        labels: dates,
        datasets: [
          {
            label: "Avg Quality Score",
            data: data.data.map((d) => d.avg_quality),
            borderColor: "#34d399",
            backgroundColor: "#34d399",
            fill: false,
            tension: 0.3,
            yAxisID: "y",
          },
          {
            label: "Degradation Events",
            data: data.data.map((d) => d.total_degradations),
            borderColor: "#f87171",
            backgroundColor: "rgba(248, 113, 113, 0.5)",
            type: "bar",
            yAxisID: "y1",
          },
        ],
      },
      options: {
        responsive: true,
        interaction: {
          mode: "index",
          intersect: false,
        },
        scales: {
          y: {
            type: "linear",
            display: true,
            position: "left",
            title: { display: true, text: "Quality Score" },
          },
          y1: {
            type: "linear",
            display: true,
            position: "right",
            title: { display: true, text: "Degradation Events" },
            grid: { drawOnChartArea: false },
          },
        },
        plugins: {
          title: { display: true, text: "Quality Score and Degradation Events Over Time" },
        },
      },
    });

    return () => {
      chartInstance.current?.destroy();
    };
  }, [data]);

  if (error) return <div class="error">Error: {error}</div>;
  if (!data) return <div>Loading quality data...</div>;

  return (
    <div class="view">
      <h2>Quality Analytics</h2>
      <canvas ref={chartRef} />
    </div>
  );
}

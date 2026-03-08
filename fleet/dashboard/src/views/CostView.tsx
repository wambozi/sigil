import { useEffect, useRef, useState } from "preact/hooks";
import { Chart, registerables } from "chart.js";
import { fetchCost, type CostData } from "../api";

Chart.register(...registerables);

export function CostView() {
  const [data, setData] = useState<CostData | null>(null);
  const [error, setError] = useState("");
  const ratioChartRef = useRef<HTMLCanvasElement>(null);
  const costChartRef = useRef<HTMLCanvasElement>(null);
  const ratioChart = useRef<Chart | null>(null);
  const costChart = useRef<Chart | null>(null);

  useEffect(() => {
    fetchCost()
      .then(setData)
      .catch((e: Error) => setError(e.message));
  }, []);

  useEffect(() => {
    if (!data || !ratioChartRef.current || !costChartRef.current) return;

    ratioChart.current?.destroy();
    costChart.current?.destroy();

    const dates = data.data.map((d) => d.date);

    // Local-vs-cloud ratio over time
    ratioChart.current = new Chart(ratioChartRef.current, {
      type: "line",
      data: {
        labels: dates,
        datasets: [
          {
            label: "Local Ratio (%)",
            data: data.data.map((d) => d.local_ratio * 100),
            borderColor: "#34d399",
            fill: false,
            tension: 0.3,
          },
          {
            label: "Cloud Ratio (%)",
            data: data.data.map((d) => (1 - d.local_ratio) * 100),
            borderColor: "#f87171",
            fill: false,
            tension: 0.3,
          },
        ],
      },
      options: {
        responsive: true,
        scales: {
          y: { title: { display: true, text: "%" }, max: 100 },
        },
        plugins: {
          title: { display: true, text: "Local vs Cloud Routing Over Time" },
        },
      },
    });

    // Estimated cost trend
    costChart.current = new Chart(costChartRef.current, {
      type: "bar",
      data: {
        labels: dates,
        datasets: [
          {
            label: "Estimated Cloud Cost ($)",
            data: data.data.map((d) => d.estimated_cost),
            backgroundColor: "#f87171",
          },
        ],
      },
      options: {
        responsive: true,
        scales: {
          y: { title: { display: true, text: "Cost ($)" } },
        },
        plugins: {
          title: { display: true, text: "Estimated AI Cloud Cost Over Time" },
        },
      },
    });

    return () => {
      ratioChart.current?.destroy();
      costChart.current?.destroy();
    };
  }, [data]);

  if (error) return <div class="error">Error: {error}</div>;
  if (!data) return <div>Loading cost data...</div>;

  return (
    <div class="view">
      <h2>AI Cost Efficiency</h2>
      <canvas ref={ratioChartRef} />
      <canvas ref={costChartRef} />
    </div>
  );
}

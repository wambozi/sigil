import { useEffect, useRef, useState } from "preact/hooks";
import { Chart, registerables } from "chart.js";
import { fetchML, type MLData } from "../api";

Chart.register(...registerables);

export function MLView() {
  const [data, setData] = useState<MLData | null>(null);
  const [error, setError] = useState("");
  const adoptionChartRef = useRef<HTMLCanvasElement>(null);
  const speedChartRef = useRef<HTMLCanvasElement>(null);
  const predictionsChartRef = useRef<HTMLCanvasElement>(null);
  const adoptionChart = useRef<Chart | null>(null);
  const speedChart = useRef<Chart | null>(null);
  const predictionsChart = useRef<Chart | null>(null);

  useEffect(() => {
    fetchML()
      .then(setData)
      .catch((e: Error) => setError(e.message));
  }, []);

  useEffect(() => {
    if (!data || !adoptionChartRef.current || !speedChartRef.current || !predictionsChartRef.current) return;

    adoptionChart.current?.destroy();
    speedChart.current?.destroy();
    predictionsChart.current?.destroy();

    const dates = data.data.map((d) => d.date);

    // ML nodes vs total nodes (stacked bar)
    adoptionChart.current = new Chart(adoptionChartRef.current, {
      type: "bar",
      data: {
        labels: dates,
        datasets: [
          {
            label: "ML-Enabled Nodes",
            data: data.data.map((d) => d.ml_nodes),
            backgroundColor: "#a78bfa",
          },
          {
            label: "Non-ML Nodes",
            data: data.data.map((d) => d.total_nodes - d.ml_nodes),
            backgroundColor: "#94a3b8",
          },
        ],
      },
      options: {
        responsive: true,
        scales: {
          x: { stacked: true },
          y: { stacked: true, title: { display: true, text: "Nodes" } },
        },
        plugins: {
          title: { display: true, text: "ML Adoption: ML vs Non-ML Nodes" },
        },
      },
    });

    // ML speed vs non-ML speed (comparison line)
    speedChart.current = new Chart(speedChartRef.current, {
      type: "line",
      data: {
        labels: dates,
        datasets: [
          {
            label: "ML Speed Score",
            data: data.data.map((d) => d.ml_speed),
            borderColor: "#a78bfa",
            backgroundColor: "#a78bfa",
            fill: false,
            tension: 0.3,
            spanGaps: true,
          },
          {
            label: "Non-ML Speed Score",
            data: data.data.map((d) => d.non_ml_speed),
            borderColor: "#94a3b8",
            backgroundColor: "#94a3b8",
            fill: false,
            tension: 0.3,
            spanGaps: true,
          },
        ],
      },
      options: {
        responsive: true,
        scales: {
          y: { title: { display: true, text: "Speed Score" } },
        },
        plugins: {
          title: { display: true, text: "ML vs Non-ML Speed Score Comparison" },
        },
      },
    });

    // Total predictions over time
    predictionsChart.current = new Chart(predictionsChartRef.current, {
      type: "line",
      data: {
        labels: dates,
        datasets: [
          {
            label: "Total Predictions",
            data: data.data.map((d) => d.total_predictions),
            borderColor: "#60a5fa",
            backgroundColor: "rgba(96, 165, 250, 0.2)",
            fill: true,
            tension: 0.3,
          },
        ],
      },
      options: {
        responsive: true,
        scales: {
          y: { title: { display: true, text: "Predictions" } },
        },
        plugins: {
          title: { display: true, text: "ML Predictions Over Time" },
        },
      },
    });

    return () => {
      adoptionChart.current?.destroy();
      speedChart.current?.destroy();
      predictionsChart.current?.destroy();
    };
  }, [data]);

  if (error) return <div class="error">Error: {error}</div>;
  if (!data) return <div>Loading ML data...</div>;

  return (
    <div class="view">
      <h2>ML Effectiveness Analytics</h2>
      <canvas ref={adoptionChartRef} />
      <canvas ref={speedChartRef} />
      <canvas ref={predictionsChartRef} />
    </div>
  );
}

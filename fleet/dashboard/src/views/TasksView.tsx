import { useEffect, useRef, useState } from "preact/hooks";
import { Chart, registerables } from "chart.js";
import { fetchTasks, type TasksData } from "../api";

Chart.register(...registerables);

export function TasksView() {
  const [data, setData] = useState<TasksData | null>(null);
  const [error, setError] = useState("");
  const chartRef = useRef<HTMLCanvasElement>(null);
  const chartInstance = useRef<Chart | null>(null);

  useEffect(() => {
    fetchTasks()
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
            label: "Avg Completed",
            data: data.data.map((d) => d.avg_completed),
            borderColor: "#34d399",
            backgroundColor: "#34d399",
            fill: false,
            tension: 0.3,
            yAxisID: "y",
          },
          {
            label: "Avg Duration (min)",
            data: data.data.map((d) => d.avg_duration),
            borderColor: "#60a5fa",
            backgroundColor: "#60a5fa",
            fill: false,
            tension: 0.3,
            yAxisID: "y",
          },
          {
            label: "Avg Speed Score",
            data: data.data.map((d) => d.avg_speed),
            borderColor: "#a78bfa",
            backgroundColor: "#a78bfa",
            fill: false,
            tension: 0.3,
            yAxisID: "y",
          },
          {
            label: "Stuck Rate",
            data: data.data.map((d) => d.stuck_rate * 100),
            borderColor: "#f87171",
            backgroundColor: "#f87171",
            borderDash: [5, 5],
            fill: false,
            tension: 0.3,
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
            title: { display: true, text: "Value" },
          },
          y1: {
            type: "linear",
            display: true,
            position: "right",
            title: { display: true, text: "Stuck Rate (%)" },
            max: 100,
            grid: { drawOnChartArea: false },
          },
        },
        plugins: {
          title: { display: true, text: "Task Velocity Over Time" },
        },
      },
    });

    return () => {
      chartInstance.current?.destroy();
    };
  }, [data]);

  if (error) return <div class="error">Error: {error}</div>;
  if (!data) return <div>Loading tasks data...</div>;

  return (
    <div class="view">
      <h2>Task Velocity Analytics</h2>
      <canvas ref={chartRef} />
    </div>
  );
}

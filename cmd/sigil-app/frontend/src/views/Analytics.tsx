import { useState, useEffect } from "preact/hooks";
import { AcceptanceChart } from "../components/AcceptanceChart";
import { DailyChart } from "../components/DailyChart";
import { CategoryChart } from "../components/CategoryChart";
import { HeatmapChart } from "../components/HeatmapChart";

declare const window: Window & {
  go: {
    main: {
      App: {
        GetAnalytics(days: number): Promise<any>;
        ExportSuggestions(format: string, from: string, to: string): Promise<string>;
      };
    };
  };
};

const RANGES = [
  { label: "7d", days: 7 },
  { label: "30d", days: 30 },
  { label: "90d", days: 90 },
];

export function Analytics() {
  const [data, setData] = useState<any>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [days, setDays] = useState(30);
  const [exporting, setExporting] = useState(false);

  const fetchData = (d: number) => {
    setLoading(true);
    setError(null);
    window.go.main.App.GetAnalytics(d)
      .then(setData)
      .catch(() => setError("Could not fetch analytics. Is the daemon running?"))
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    fetchData(days);
  }, [days]);

  const handleExport = async (format: string) => {
    setExporting(true);
    try {
      const content = await window.go.main.App.ExportSuggestions(format, "", "");
      // Trigger download via blob URL.
      const blob = new Blob([content], {
        type: format === "csv" ? "text/csv" : "application/json",
      });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `sigil-suggestions.${format}`;
      a.click();
      URL.revokeObjectURL(url);
    } catch {
      // Silently fail — daemon may be unavailable.
    } finally {
      setExporting(false);
    }
  };

  if (loading) {
    return (
      <div class="analytics-view">
        <div class="empty-state">
          <div class="loading-spinner" />
        </div>
      </div>
    );
  }

  if (error) {
    return (
      <div class="analytics-view">
        <div class="empty-state">
          <div class="empty-state-title">Unavailable</div>
          <div class="empty-state-text">{error}</div>
        </div>
      </div>
    );
  }

  if (!data) return null;

  return (
    <div class="analytics-view">
      {/* Range selector */}
      <div class="analytics-controls">
        <div class="filter-bar" style={{ borderBottom: "none", padding: "8px 0" }}>
          {RANGES.map((r) => (
            <button
              key={r.days}
              class={`filter-btn ${days === r.days ? "active" : ""}`}
              onClick={() => setDays(r.days)}
            >
              {r.label}
            </button>
          ))}
        </div>
        <div class="analytics-export">
          <button
            class="btn"
            onClick={() => handleExport("csv")}
            disabled={exporting}
          >
            CSV
          </button>
          <button
            class="btn"
            onClick={() => handleExport("json")}
            disabled={exporting}
          >
            JSON
          </button>
        </div>
      </div>

      {/* Streak */}
      {data.streak_days > 0 && (
        <div class="analytics-streak">
          {data.streak_days} day streak
        </div>
      )}

      {/* Charts */}
      <AcceptanceChart data={data.daily_counts || []} />
      <DailyChart data={data.daily_counts || []} />
      <CategoryChart data={data.category_breakdown || []} />
      <HeatmapChart data={data.hourly_distribution || new Array(24).fill(0)} />
    </div>
  );
}

import { useState, useEffect } from "preact/hooks";

declare const window: Window & {
  go: {
    main: {
      App: {
        GetDaySummary(): Promise<any>;
      };
    };
  };
};

export function DaySummary() {
  const [summary, setSummary] = useState<any>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    setLoading(true);
    window.go.main.App.GetDaySummary()
      .then((data) => {
        setSummary(data);
        setError(null);
      })
      .catch(() => {
        setError("Could not fetch day summary. Is the daemon running?");
      })
      .finally(() => setLoading(false));
  }, []);

  if (loading) {
    return (
      <div class="summary-view">
        <div class="empty-state">
          <div class="loading-spinner" />
        </div>
      </div>
    );
  }

  if (error) {
    return (
      <div class="summary-view">
        <div class="empty-state">
          <div class="empty-state-title">Unavailable</div>
          <div class="empty-state-text">{error}</div>
        </div>
      </div>
    );
  }

  if (!summary) {
    return (
      <div class="summary-view">
        <div class="empty-state">
          <div class="empty-state-icon">&#128202;</div>
          <div class="empty-state-title">No data yet</div>
          <div class="empty-state-text">
            Activity data will appear after Sigil has been observing for a while.
          </div>
        </div>
      </div>
    );
  }

  // Render whatever fields the daemon returns.
  const entries = Object.entries(summary);

  return (
    <div class="summary-view">
      <div class="summary-section">
        <div class="summary-section-title">Today's Activity</div>
        {entries.map(([key, value]) => (
          <div class="summary-item" key={key}>
            <span>{formatKey(key)}</span>
            <span class="summary-value">{formatValue(value)}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

function formatKey(key: string): string {
  return key
    .replace(/_/g, " ")
    .replace(/\b\w/g, (c) => c.toUpperCase());
}

function formatValue(value: unknown): string {
  if (typeof value === "number") {
    return value.toLocaleString();
  }
  if (typeof value === "object" && value !== null) {
    return JSON.stringify(value);
  }
  return String(value ?? "-");
}

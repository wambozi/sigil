import { useState, useEffect } from "preact/hooks";

declare const window: Window & {
  go: {
    main: {
      App: {
        GetDaySummary(): Promise<any>;
        GetStatus(): Promise<any>;
        GetCurrentTask(): Promise<any>;
      };
    };
  };
};

export function DaySummary() {
  const [summary, setSummary] = useState<any>(null);
  const [status, setStatus] = useState<any>(null);
  const [task, setTask] = useState<any>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const refresh = () => {
    setLoading(true);
    Promise.all([
      window.go.main.App.GetDaySummary().catch(() => null),
      window.go.main.App.GetStatus().catch(() => null),
      window.go.main.App.GetCurrentTask().catch(() => null),
    ])
      .then(([sum, st, tk]) => {
        setSummary(sum);
        setStatus(st);
        setTask(tk);
        setError(null);
      })
      .catch(() => setError("Could not fetch data. Is the daemon running?"))
      .finally(() => setLoading(false));
  };

  useEffect(() => { refresh(); }, []);

  if (loading) {
    return (
      <div class="summary-view">
        <div class="empty-state"><div class="loading-spinner" /></div>
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

  const s = summary || {};
  const st = status || {};

  return (
    <div class="summary-view">
      {/* Current Task */}
      {task && task.id && (
        <div class="summary-card current-task-card">
          <div class="summary-card-header">
            <span class="summary-card-icon">&#9654;</span>
            <span class="summary-card-title">Current Task</span>
            <span class={`phase-badge phase-${task.phase || "unknown"}`}>
              {task.phase || "idle"}
            </span>
          </div>
          <div class="current-task-details">
            <div class="task-repo">{shortenPath(task.repo_root)}</div>
            {task.branch && <div class="task-branch">{task.branch}</div>}
            <div class="task-meta">
              {task.elapsed_min != null && (
                <span>{task.elapsed_min}m elapsed</span>
              )}
              {task.files && task.files.length > 0 && (
                <span>{task.files.length} files touched</span>
              )}
              {task.test_runs > 0 && (
                <span>{task.test_runs} test runs ({task.test_failures} failed)</span>
              )}
            </div>
          </div>
        </div>
      )}

      {/* Stats Grid */}
      <div class="stats-grid">
        <StatCard
          icon="&#128221;"
          label="Editing"
          value={formatDuration(s.editing_minutes)}
        />
        <StatCard
          icon="&#9989;"
          label="Verifying"
          value={formatDuration(s.verifying_minutes)}
        />
        <StatCard
          icon="&#128680;"
          label="Stuck"
          value={formatDuration(s.stuck_minutes)}
          warn={s.stuck_minutes > 30}
        />
        <StatCard
          icon="&#128640;"
          label="Speed"
          value={s.speed_score ? `${s.speed_score.toFixed(1)}/min` : "—"}
        />
      </div>

      <div class="stats-grid">
        <StatCard
          icon="&#128194;"
          label="Files"
          value={String(s.files_touched || 0)}
        />
        <StatCard
          icon="&#128229;"
          label="Commits"
          value={String(s.total_commits || 0)}
        />
        <StatCard
          icon="&#9989;"
          label="Tasks Done"
          value={`${s.tasks_completed || 0} / ${s.tasks_started || 0}`}
        />
        <StatCard
          icon="&#128340;"
          label="Uptime"
          value={formatDuration(Math.round((st.uptime_seconds || 0) / 60))}
        />
      </div>

      {/* Repos */}
      {s.repos && s.repos.length > 0 && (
        <div class="summary-section">
          <div class="summary-section-title">Repositories</div>
          {s.repos.map((repo: string) => (
            <div class="summary-repo-item" key={repo}>{shortenPath(repo)}</div>
          ))}
        </div>
      )}

      {/* Tasks */}
      {s.tasks && s.tasks.length > 0 && (
        <div class="summary-section">
          <div class="summary-section-title">Tasks Today</div>
          {s.tasks.map((t: any, i: number) => (
            <div class="summary-task-item" key={i}>
              <span class="task-name">{t.description || t.branch || t.id}</span>
              <span class="task-duration">{formatDuration(t.duration_min)}</span>
            </div>
          ))}
        </div>
      )}

      {/* Daemon Info */}
      <div class="summary-section summary-footer">
        <div class="summary-footer-row">
          <span>Sigil {st.version || "—"}</span>
          <span>RSS {st.rss_mb || 0}MB</span>
          <span>Level {st.notifier_level ?? "—"}</span>
          <span>{s.date || "—"}</span>
        </div>
      </div>

      <div class="summary-refresh">
        <button class="btn daemon-btn" onClick={refresh}>Refresh</button>
      </div>
    </div>
  );
}

function StatCard({ icon, label, value, warn }: { icon: string; label: string; value: string; warn?: boolean }) {
  return (
    <div class={`stat-card ${warn ? "stat-warn" : ""}`}>
      <div class="stat-icon">{icon}</div>
      <div class="stat-value">{value}</div>
      <div class="stat-label">{label}</div>
    </div>
  );
}

function formatDuration(minutes: number | undefined | null): string {
  if (!minutes || minutes === 0) return "0m";
  if (minutes < 60) return `${Math.round(minutes)}m`;
  const h = Math.floor(minutes / 60);
  const m = Math.round(minutes % 60);
  return m > 0 ? `${h}h ${m}m` : `${h}h`;
}

function shortenPath(p: string | undefined): string {
  if (!p) return "—";
  const home = "/Users/";
  const idx = p.indexOf(home);
  if (idx >= 0) {
    const afterHome = p.substring(idx + home.length);
    const slashIdx = afterHome.indexOf("/");
    if (slashIdx >= 0) return "~" + afterHome.substring(slashIdx);
  }
  return p.split("/").slice(-2).join("/");
}

import { useState, useEffect } from "preact/hooks";

declare const window: Window & {
  go: {
    main: {
      App: {
        GetHealth(): Promise<{
          services: {
            name: string;
            status: string;
            message: string;
            actions?: { label: string; action: string }[];
          }[];
        }>;
        SetConfig(cfg: any): Promise<any>;
        CloudSignIn(): Promise<void>;
        RestartDaemon(): Promise<void>;
      };
    };
  };
};

const STATUS_DOT: Record<string, string> = {
  ok: "var(--success)",
  degraded: "var(--warning)",
  down: "var(--danger)",
  disabled: "var(--fg-secondary)",
};

export function HealthPanel() {
  const [services, setServices] = useState<any[]>([]);
  const [loading, setLoading] = useState(true);
  const [acting, setActing] = useState<string | null>(null);

  const refresh = () => {
    window.go.main.App.GetHealth()
      .then((r) => setServices(r.services || []))
      .catch(() => setServices([]))
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    refresh();
    const interval = setInterval(refresh, 30000);
    return () => clearInterval(interval);
  }, []);

  const handleAction = async (action: string) => {
    setActing(action);
    try {
      switch (action) {
        case "enable_cloud_llm":
          await window.go.main.App.SetConfig({
            inference: { mode: "remotefirst", cloud: { enabled: true } },
          });
          await window.go.main.App.CloudSignIn();
          break;
        case "enable_local_llm":
          await window.go.main.App.SetConfig({
            inference: { mode: "local", local: { enabled: true } },
          });
          break;
        case "disable_llm":
          await window.go.main.App.SetConfig({
            inference: { local: { enabled: false }, cloud: { enabled: false } },
          });
          break;
        case "cloud_signin":
          await window.go.main.App.CloudSignIn();
          break;
        case "enable_ml":
          await window.go.main.App.SetConfig({
            ml: { mode: "local", local: { enabled: true } },
          });
          break;
        case "enable_cloud_ml":
          await window.go.main.App.SetConfig({
            ml: { mode: "remotefirst", cloud: { enabled: true } },
          });
          await window.go.main.App.CloudSignIn();
          break;
        case "disable_ml":
          await window.go.main.App.SetConfig({ ml: { mode: "disabled" } });
          break;
        case "restart_daemon":
          await window.go.main.App.RestartDaemon();
          break;
      }
    } catch {
      // Action failed — refresh will show current state.
    } finally {
      setActing(null);
      // Refresh after a beat to let config changes take effect.
      setTimeout(refresh, 1000);
    }
  };

  if (loading) return null;

  // Only show the panel if there are issues.
  const issues = services.filter(
    (s) => s.status === "down" || s.status === "degraded"
  );
  if (issues.length === 0) return null;

  return (
    <div class="health-panel has-issues">
      {issues.map((svc) => (
        <div key={svc.name} class="health-issue-card">
          <div class="health-issue-header">
            <span
              class="health-dot"
              style={{ background: STATUS_DOT[svc.status] }}
            />
            <span class="health-issue-name">{svc.name}</span>
          </div>
          <div class="health-issue-msg">{svc.message}</div>
          {svc.actions && svc.actions.length > 0 && (
            <div class="health-actions">
              {svc.actions.map(
                (a: { label: string; action: string }, i: number) => (
                  <button
                    key={a.action}
                    type="button"
                    class={`btn health-action-btn ${i === 0 ? "btn-primary" : ""}`}
                    onClick={() => handleAction(a.action)}
                    disabled={acting !== null}
                  >
                    {acting === a.action ? "Working..." : a.label}
                  </button>
                )
              )}
            </div>
          )}
        </div>
      ))}
    </div>
  );
}

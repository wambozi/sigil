import { useState, useEffect, useCallback } from "preact/hooks";
import { PluginCard } from "../components/PluginCard";

declare const window: Window & {
  go: {
    main: {
      App: {
        GetPluginStatus(): Promise<any[]>;
        GetPluginRegistry(): Promise<any[]>;
        InstallPlugin(name: string): Promise<void>;
        EnablePlugin(name: string): Promise<void>;
        DisablePlugin(name: string): Promise<void>;
      };
    };
  };
};

type PluginFilter = "all" | "installed" | "available";

export function Plugins() {
  const [installed, setInstalled] = useState<any[]>([]);
  const [registry, setRegistry] = useState<any[]>([]);
  const [filter, setFilter] = useState<PluginFilter>("all");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [installing, setInstalling] = useState<Set<string>>(new Set());

  const fetchData = useCallback(async () => {
    try {
      const [status, reg] = await Promise.all([
        window.go.main.App.GetPluginStatus(),
        window.go.main.App.GetPluginRegistry(),
      ]);
      setInstalled(Array.isArray(status) ? status : []);
      setRegistry(Array.isArray(reg) ? reg : []);
      setError(null);
    } catch {
      setError("Could not fetch plugin data. Is the daemon running?");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    setLoading(true);
    fetchData();
  }, [fetchData]);

  const handleToggle = async (name: string, enabled: boolean) => {
    try {
      if (enabled) {
        await window.go.main.App.EnablePlugin(name);
      } else {
        await window.go.main.App.DisablePlugin(name);
      }
      await fetchData();
    } catch {
      // Toggle failed silently.
    }
  };

  const handleInstall = async (name: string) => {
    setInstalling((prev) => new Set(prev).add(name));
    try {
      await window.go.main.App.InstallPlugin(name);
      await fetchData();
    } catch {
      // Install failed silently.
    } finally {
      setInstalling((prev) => {
        const next = new Set(prev);
        next.delete(name);
        return next;
      });
    }
  };

  const installedNames = new Set(installed.map((p: any) => p.name));

  const availablePlugins = registry.filter(
    (p: any) => !installedNames.has(p.name)
  );

  const visibleInstalled = filter !== "available" ? installed : [];
  const visibleAvailable = filter !== "installed" ? availablePlugins : [];

  if (loading) {
    return (
      <div class="settings-view">
        <div class="empty-state">
          <div class="loading-spinner" />
        </div>
      </div>
    );
  }

  if (error) {
    return (
      <div class="settings-view">
        <div class="empty-state">
          <div class="empty-state-title">Unavailable</div>
          <div class="empty-state-text">{error}</div>
        </div>
      </div>
    );
  }

  return (
    <div class="settings-view">
      <div class="filter-bar">
        {(["all", "installed", "available"] as PluginFilter[]).map((f) => (
          <button
            key={f}
            class={`filter-btn ${filter === f ? "active" : ""}`}
            onClick={() => setFilter(f)}
          >
            {f.charAt(0).toUpperCase() + f.slice(1)}
          </button>
        ))}
      </div>

      {visibleInstalled.length === 0 && visibleAvailable.length === 0 && (
        <div class="empty-state">
          <div class="empty-state-icon">&#128268;</div>
          <div class="empty-state-title">No plugins</div>
          <div class="empty-state-text">
            {filter === "installed"
              ? "No plugins are installed yet."
              : filter === "available"
              ? "No new plugins available."
              : "No plugins found."}
          </div>
        </div>
      )}

      {visibleInstalled.length > 0 && (
        <div class="settings-section">
          <h3>Installed</h3>
          {visibleInstalled.map((p: any) => (
            <PluginCard
              key={p.name}
              name={p.name}
              description={p.description || ""}
              version={p.version}
              category={p.category}
              installed={true}
              enabled={p.enabled}
              running={p.running}
              healthy={p.healthy}
              daemon={p.daemon}
              onToggle={(enabled: boolean) => handleToggle(p.name, enabled)}
            />
          ))}
        </div>
      )}

      {visibleAvailable.length > 0 && (
        <div class="settings-section">
          <h3>Available</h3>
          {visibleAvailable.map((p: any) => (
            <PluginCard
              key={p.name}
              name={p.name}
              description={p.description || ""}
              version={p.version}
              category={p.category}
              installed={false}
              onInstall={() => handleInstall(p.name)}
              installing={installing.has(p.name)}
            />
          ))}
        </div>
      )}
    </div>
  );
}

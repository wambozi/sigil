import { useState, useEffect } from "preact/hooks";
import { Toggle } from "../components/Toggle";
import { EditableList } from "../components/EditableList";

declare const window: Window & {
  go: {
    main: {
      App: {
        GetConfig(): Promise<any>;
        SetConfig(config: any): Promise<any>;
        StopDaemon(): Promise<void>;
        StartDaemon(): Promise<void>;
        RestartDaemon(): Promise<void>;
      };
    };
  };
};

const LOG_LEVELS = ["debug", "info", "warn", "error"];
const NOTIFICATION_LABELS: Record<number, string> = {
  0: "0 - Silent",
  1: "1 - Digest",
  2: "2 - Ambient",
  3: "3 - Conversational",
  4: "4 - Autonomous",
};
const INFERENCE_MODES = ["local", "localfirst", "remotefirst", "remote"];
const ML_MODES = ["local", "localfirst", "remotefirst", "remote", "disabled"];

export function Settings() {
  const [config, setConfig] = useState<any>(null);
  const [original, setOriginal] = useState<any>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [banner, setBanner] = useState<{ type: "success" | "error"; message: string } | null>(null);
  const [restartRequired, setRestartRequired] = useState(false);
  const [daemonAction, setDaemonAction] = useState<string | null>(null);

  useEffect(() => {
    setLoading(true);
    window.go.main.App.GetConfig()
      .then((data) => {
        setConfig(structuredClone(data));
        setOriginal(structuredClone(data));
        setError(null);
      })
      .catch(() => {
        setError("Could not fetch configuration. Is the daemon running?");
      })
      .finally(() => setLoading(false));
  }, []);

  const isDirty = () => {
    return JSON.stringify(config) !== JSON.stringify(original);
  };

  const update = (path: string, value: any) => {
    setConfig((prev: any) => {
      const next = structuredClone(prev);
      const parts = path.split(".");
      let obj = next;
      for (let i = 0; i < parts.length - 1; i++) {
        if (!obj[parts[i]]) obj[parts[i]] = {};
        obj = obj[parts[i]];
      }
      obj[parts[parts.length - 1]] = value;
      return next;
    });
  };

  const handleSave = async () => {
    if (!isDirty() || saving) return;
    setSaving(true);
    setBanner(null);
    setRestartRequired(false);

    try {
      const result = await window.go.main.App.SetConfig(config);
      setOriginal(structuredClone(config));
      if (result && result.restart_required) {
        setRestartRequired(true);
      }
      setBanner({ type: "success", message: "Settings saved successfully." });
    } catch {
      setBanner({ type: "error", message: "Failed to save settings." });
    } finally {
      setSaving(false);
    }
  };

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

  if (!config) return null;

  const daemon = config.daemon || {};
  const notifier = config.notifier || {};
  const inference = config.inference || {};
  const ml = config.ml || {};
  const retention = config.retention || {};
  const fleet = config.fleet || {};

  return (
    <div class="settings-view">
      {banner && (
        <div class={`save-banner ${banner.type}`}>{banner.message}</div>
      )}
      {restartRequired && (
        <div class="restart-notice">
          Some changes require a daemon restart to take effect.
        </div>
      )}

      {/* Daemon Control */}
      <div class="settings-section">
        <h3>Daemon</h3>
        <div class="settings-row daemon-controls">
          <button
            class="btn daemon-btn"
            onClick={async () => {
              setDaemonAction("starting");
              try {
                await window.go.main.App.StartDaemon();
                setBanner({ type: "success", message: "Daemon started." });
              } catch {
                setBanner({ type: "error", message: "Failed to start daemon." });
              }
              setDaemonAction(null);
            }}
            disabled={!!daemonAction}
          >
            {daemonAction === "starting" ? "Starting..." : "Start"}
          </button>
          <button
            class="btn daemon-btn daemon-btn-warn"
            onClick={async () => {
              setDaemonAction("stopping");
              try {
                await window.go.main.App.StopDaemon();
                setBanner({ type: "success", message: "Daemon stopped." });
              } catch {
                setBanner({ type: "error", message: "Failed to stop daemon." });
              }
              setDaemonAction(null);
            }}
            disabled={!!daemonAction}
          >
            {daemonAction === "stopping" ? "Stopping..." : "Stop"}
          </button>
          <button
            class="btn daemon-btn"
            onClick={async () => {
              setDaemonAction("restarting");
              try {
                await window.go.main.App.RestartDaemon();
                setBanner({ type: "success", message: "Daemon restarted." });
              } catch {
                setBanner({ type: "error", message: "Failed to restart daemon." });
              }
              setDaemonAction(null);
            }}
            disabled={!!daemonAction}
          >
            {daemonAction === "restarting" ? "Restarting..." : "Restart"}
          </button>
        </div>
      </div>

      {/* General */}
      <div class="settings-section">
        <h3>General</h3>
        <div class="settings-row">
          <label class="settings-label">Log Level</label>
          <select
            class="settings-select"
            value={daemon.log_level || "info"}
            onChange={(e) =>
              update("daemon.log_level", (e.target as HTMLSelectElement).value)
            }
          >
            {LOG_LEVELS.map((l) => (
              <option key={l} value={l}>{l}</option>
            ))}
          </select>
        </div>
        <div class="settings-row">
          <label class="settings-label">Watch Directories</label>
          <EditableList
            items={daemon.watch_dirs || []}
            onChange={(dirs) => update("daemon.watch_dirs", dirs)}
            placeholder="Add directory path..."
          />
        </div>
        <div class="settings-row">
          <Toggle
            label="Actuations Enabled"
            checked={daemon.actuations_enabled ?? false}
            onChange={(v) => update("daemon.actuations_enabled", v)}
          />
        </div>
      </div>

      {/* Notifications */}
      <div class="settings-section">
        <h3>Notifications</h3>
        <div class="settings-row">
          <label class="settings-label">Level</label>
          <select
            class="settings-select"
            value={notifier.level ?? 2}
            onChange={(e) =>
              update("notifier.level", parseInt((e.target as HTMLSelectElement).value, 10))
            }
          >
            {[0, 1, 2, 3, 4].map((n) => (
              <option key={n} value={n}>{NOTIFICATION_LABELS[n]}</option>
            ))}
          </select>
        </div>
        <div class="settings-row">
          <label class="settings-label">Digest Time</label>
          <input
            class="settings-input"
            type="text"
            value={notifier.digest_time || ""}
            onInput={(e) =>
              update("notifier.digest_time", (e.target as HTMLInputElement).value)
            }
            placeholder="e.g. 09:00"
          />
        </div>
      </div>

      {/* LLM Inference */}
      <div class="settings-section">
        <h3>LLM Inference</h3>
        <div class="settings-row">
          <label class="settings-label">Mode</label>
          <select
            class="settings-select"
            value={inference.mode || "localfirst"}
            onChange={(e) =>
              update("inference.mode", (e.target as HTMLSelectElement).value)
            }
          >
            {INFERENCE_MODES.map((m) => (
              <option key={m} value={m}>{m}</option>
            ))}
          </select>
        </div>
        <div class="settings-row">
          <Toggle
            label="Local Enabled"
            checked={inference.local?.enabled ?? false}
            onChange={(v) => update("inference.local.enabled", v)}
          />
        </div>
        <div class="settings-row">
          <label class="settings-label">Local Server URL</label>
          <input
            class="settings-input"
            type="text"
            value={inference.local?.server_url || ""}
            onInput={(e) =>
              update("inference.local.server_url", (e.target as HTMLInputElement).value)
            }
            placeholder="http://127.0.0.1:11434"
          />
        </div>
        <div class="settings-row">
          <Toggle
            label="Cloud Enabled"
            checked={inference.cloud?.enabled ?? false}
            onChange={(v) => update("inference.cloud.enabled", v)}
          />
        </div>
        <div class="settings-row">
          <label class="settings-label">Cloud Provider</label>
          <select
            class="settings-select"
            value={inference.cloud?.provider || ""}
            onChange={(e) =>
              update("inference.cloud.provider", (e.target as HTMLSelectElement).value)
            }
          >
            <option value="">Select...</option>
            <option value="anthropic">Anthropic</option>
            <option value="openai">OpenAI</option>
          </select>
        </div>
        <div class="settings-row">
          <label class="settings-label">Cloud API Key</label>
          <input
            class="settings-input"
            type="password"
            value={inference.cloud?.api_key || ""}
            onInput={(e) =>
              update("inference.cloud.api_key", (e.target as HTMLInputElement).value)
            }
            placeholder="Enter API key..."
          />
        </div>
      </div>

      {/* ML Pipeline */}
      <div class="settings-section">
        <h3>ML Pipeline</h3>
        <div class="settings-row">
          <label class="settings-label">Mode</label>
          <select
            class="settings-select"
            value={ml.mode || "local"}
            onChange={(e) =>
              update("ml.mode", (e.target as HTMLSelectElement).value)
            }
          >
            {ML_MODES.map((m) => (
              <option key={m} value={m}>{m}</option>
            ))}
          </select>
        </div>
        <div class="settings-row">
          <Toggle
            label="Local Enabled"
            checked={ml.local?.enabled ?? false}
            onChange={(v) => update("ml.local.enabled", v)}
          />
        </div>
        <div class="settings-row">
          <label class="settings-label">Local Server URL</label>
          <input
            class="settings-input"
            type="text"
            value={ml.local?.server_url || ""}
            onInput={(e) =>
              update("ml.local.server_url", (e.target as HTMLInputElement).value)
            }
            placeholder="http://127.0.0.1:7774"
          />
        </div>
        <div class="settings-row">
          <Toggle
            label="Cloud Enabled"
            checked={ml.cloud?.enabled ?? false}
            onChange={(v) => update("ml.cloud.enabled", v)}
          />
        </div>
        <div class="settings-row">
          <label class="settings-label">Cloud API Key</label>
          <input
            class="settings-input"
            type="password"
            value={ml.cloud?.api_key || ""}
            onInput={(e) =>
              update("ml.cloud.api_key", (e.target as HTMLInputElement).value)
            }
            placeholder="Enter API key..."
          />
        </div>
      </div>

      {/* Data */}
      <div class="settings-section">
        <h3>Data</h3>
        <div class="settings-row">
          <label class="settings-label">Raw Event Days</label>
          <input
            class="settings-input"
            type="number"
            value={retention.raw_event_days ?? 30}
            onInput={(e) =>
              update("retention.raw_event_days", parseInt((e.target as HTMLInputElement).value, 10))
            }
            min={1}
          />
        </div>
        <div class="settings-row">
          <label class="settings-label">Analyze Every</label>
          <input
            class="settings-input"
            type="text"
            value={daemon.analyze_every || ""}
            onInput={(e) =>
              update("daemon.analyze_every", (e.target as HTMLInputElement).value)
            }
            placeholder="e.g. 1h"
          />
        </div>
      </div>

      {/* Fleet */}
      <div class="settings-section">
        <h3>Fleet</h3>
        <div class="settings-row">
          <Toggle
            label="Enabled"
            checked={fleet.enabled ?? false}
            onChange={(v) => update("fleet.enabled", v)}
          />
        </div>
        <div class="settings-row">
          <label class="settings-label">Endpoint</label>
          <input
            class="settings-input"
            type="text"
            value={fleet.endpoint || ""}
            onInput={(e) =>
              update("fleet.endpoint", (e.target as HTMLInputElement).value)
            }
            placeholder="https://fleet.example.com"
          />
        </div>
      </div>

      <div class="settings-save-area">
        <button
          class="btn save-btn"
          onClick={handleSave}
          disabled={!isDirty() || saving}
        >
          {saving ? "Saving..." : "Save Changes"}
        </button>
      </div>
    </div>
  );
}

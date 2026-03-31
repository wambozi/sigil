import { useState, useEffect } from "preact/hooks";
import { Toggle } from "../components/Toggle";
import { EditableList } from "../components/EditableList";
import { CloudSettings } from "../components/CloudStatus";

declare const window: Window & {
  go: {
    main: {
      App: {
        GetConfig(): Promise<any>;
        SetConfig(config: any): Promise<any>;
        StopDaemon(): Promise<void>;
        StartDaemon(): Promise<void>;
        RestartDaemon(): Promise<void>;
        GetVersion(): Promise<string>;
        CheckForUpdate(): Promise<any | null>;
        SetUpdateMode(mode: string): Promise<void>;
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

export function Settings({ onRerunSetup, connected }: { onRerunSetup?: () => void; connected?: boolean }) {
  const [config, setConfig] = useState<any>(null);
  const [original, setOriginal] = useState<any>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [banner, setBanner] = useState<{
    type: "success" | "error";
    message: string;
  } | null>(null);
  const [restartRequired, setRestartRequired] = useState(false);
  const [daemonAction, setDaemonAction] = useState<string | null>(null);
  const [appVersion, setAppVersion] = useState<string>("");
  const [updateCheckMsg, setUpdateCheckMsg] = useState<string | null>(null);
  const [checkingUpdate, setCheckingUpdate] = useState(false);

  useEffect(() => {
    window.go.main.App.GetVersion().then(setAppVersion).catch(() => {});
  }, []);

  const handleCheckUpdate = async () => {
    setCheckingUpdate(true);
    setUpdateCheckMsg(null);
    try {
      const info = await window.go.main.App.CheckForUpdate();
      if (info && info.version) {
        setUpdateCheckMsg(`Update available: v${info.version}`);
      } else {
        setUpdateCheckMsg("You are up to date.");
      }
    } catch {
      setUpdateCheckMsg("Failed to check for updates.");
    } finally {
      setCheckingUpdate(false);
    }
  };

  const fetchConfig = () => {
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
  };

  useEffect(() => {
    fetchConfig();
  }, []);

  // Re-fetch config when daemon connection is restored.
  useEffect(() => {
    if (connected && error) {
      fetchConfig();
    }
  }, [connected]);

  const isDirty = () => JSON.stringify(config) !== JSON.stringify(original);

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
      setBanner({ type: "success", message: "Settings saved." });
    } catch {
      setBanner({ type: "error", message: "Failed to save settings." });
    } finally {
      setSaving(false);
    }
  };

  const daemonCmd = async (
    action: string,
    fn: () => Promise<void>,
    msg: string
  ) => {
    setDaemonAction(action);
    try {
      await fn();
      setBanner({ type: "success", message: msg });
    } catch {
      setBanner({ type: "error", message: `Failed to ${action} daemon.` });
    }
    setDaemonAction(null);
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
          <div class="empty-state-title">Daemon Not Running</div>
          <div class="empty-state-text">{error}</div>
        </div>
        <div class="settings-section">
          <h3>Daemon</h3>
          <div class="settings-card">
            <div class="settings-row daemon-controls">
              <button
                class="btn daemon-btn"
                onClick={() =>
                  daemonCmd(
                    "start",
                    async () => {
                      await window.go.main.App.StartDaemon();
                      // Re-fetch config after a short delay to let daemon start.
                      setTimeout(() => {
                        setError(null);
                        setLoading(true);
                        window.go.main.App.GetConfig()
                          .then((data) => {
                            setConfig(structuredClone(data));
                            setOriginal(structuredClone(data));
                            setError(null);
                          })
                          .catch(() => setError("Daemon started but config not yet available. Try refreshing."))
                          .finally(() => setLoading(false));
                      }, 2000);
                    },
                    "Daemon starting..."
                  )
                }
                disabled={!!daemonAction}
              >
                {daemonAction === "start" ? "Starting..." : "Start Daemon"}
              </button>
              <button
                class="btn daemon-btn"
                onClick={() => {
                  setError(null);
                  setLoading(true);
                  window.go.main.App.GetConfig()
                    .then((data) => {
                      setConfig(structuredClone(data));
                      setOriginal(structuredClone(data));
                      setError(null);
                    })
                    .catch(() => setError("Could not fetch configuration. Is the daemon running?"))
                    .finally(() => setLoading(false));
                }}
              >
                Retry
              </button>
            </div>
          </div>
        </div>
        {onRerunSetup && (
          <div class="settings-save-area">
            <button class="btn daemon-btn" onClick={onRerunSetup}>
              Re-run Setup Wizard
            </button>
          </div>
        )}
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
        <div class="settings-card">
          <div class="settings-row daemon-controls">
            <button
              class="btn daemon-btn"
              onClick={() =>
                daemonCmd(
                  "start",
                  () => window.go.main.App.StartDaemon(),
                  "Daemon started."
                )
              }
              disabled={!!daemonAction}
            >
              {daemonAction === "start" ? "Starting..." : "Start"}
            </button>
            <button
              class="btn daemon-btn daemon-btn-warn"
              onClick={() =>
                daemonCmd(
                  "stop",
                  () => window.go.main.App.StopDaemon(),
                  "Daemon stopped."
                )
              }
              disabled={!!daemonAction}
            >
              {daemonAction === "stop" ? "Stopping..." : "Stop"}
            </button>
            <button
              class="btn daemon-btn"
              onClick={() =>
                daemonCmd(
                  "restart",
                  () => window.go.main.App.RestartDaemon(),
                  "Daemon restarted."
                )
              }
              disabled={!!daemonAction}
            >
              {daemonAction === "restart" ? "Restarting..." : "Restart"}
            </button>
          </div>
        </div>
      </div>

      {/* General */}
      <div class="settings-section">
        <h3>General</h3>
        <div class="settings-card">
          <div class="settings-row">
            <span class="settings-label">Log Level</span>
            <select
              class="settings-select"
              value={daemon.log_level || "info"}
              onChange={(e) =>
                update(
                  "daemon.log_level",
                  (e.target as HTMLSelectElement).value
                )
              }
            >
              {LOG_LEVELS.map((l) => (
                <option key={l} value={l}>
                  {l}
                </option>
              ))}
            </select>
          </div>
          <div class="settings-row">
            <div class="settings-label-group">
              <span class="settings-label">Watch Directories</span>
              <div class="settings-label-sub">
                Sigil observes these paths for file edits and git activity
              </div>
            </div>
          </div>
          <div class="settings-row" style={{ paddingTop: 0 }}>
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
      </div>

      {/* Notifications */}
      <div class="settings-section">
        <h3>Notifications</h3>
        <div class="settings-section-desc">
          Controls how aggressively Sigil surfaces suggestions.
        </div>
        <div class="settings-card">
          <div class="settings-row">
            <span class="settings-label">Level</span>
            <select
              class="settings-select"
              value={notifier.level ?? 2}
              onChange={(e) =>
                update(
                  "notifier.level",
                  parseInt((e.target as HTMLSelectElement).value, 10)
                )
              }
            >
              {[0, 1, 2, 3, 4].map((n) => (
                <option key={n} value={n}>
                  {NOTIFICATION_LABELS[n]}
                </option>
              ))}
            </select>
          </div>
          <div class="settings-row">
            <span class="settings-label">Digest Time</span>
            <input
              class="settings-input"
              type="text"
              value={notifier.digest_time || ""}
              onInput={(e) =>
                update(
                  "notifier.digest_time",
                  (e.target as HTMLInputElement).value
                )
              }
              placeholder="09:00"
            />
          </div>
        </div>
      </div>

      {/* LLM Inference */}
      <div class="settings-section">
        <h3>LLM Inference</h3>
        <div class="settings-section-desc">
          Route queries to local or cloud AI models.
        </div>
        <div class="settings-card">
          <div class="settings-row">
            <span class="settings-label">Mode</span>
            <select
              class="settings-select"
              value={inference.mode || "localfirst"}
              onChange={(e) =>
                update(
                  "inference.mode",
                  (e.target as HTMLSelectElement).value
                )
              }
            >
              {INFERENCE_MODES.map((m) => (
                <option key={m} value={m}>
                  {m}
                </option>
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
            <span class="settings-label">Local Server URL</span>
            <input
              class="settings-input"
              type="text"
              value={inference.local?.server_url || ""}
              onInput={(e) =>
                update(
                  "inference.local.server_url",
                  (e.target as HTMLInputElement).value
                )
              }
              placeholder="http://127.0.0.1:11434"
            />
          </div>
          <div class="settings-row">
            <div class="settings-label-group">
              <span class="settings-label">Cloud Inference</span>
              <div class="settings-label-sub">
                Enabled automatically when signed in to Sigil Cloud
              </div>
            </div>
            <div class="toggle-switch">
              <Toggle
                label=""
                checked={inference.cloud?.enabled ?? false}
                onChange={(v) => update("inference.cloud.enabled", v)}
              />
            </div>
          </div>
        </div>
      </div>

      {/* ML Pipeline */}
      <div class="settings-section">
        <h3>ML Pipeline</h3>
        <div class="settings-section-desc">
          Local or cloud ML for stuck detection and suggestion timing.
        </div>
        <div class="settings-card">
          <div class="settings-row">
            <span class="settings-label">Mode</span>
            <select
              class="settings-select"
              value={ml.mode || "local"}
              onChange={(e) =>
                update("ml.mode", (e.target as HTMLSelectElement).value)
              }
            >
              {ML_MODES.map((m) => (
                <option key={m} value={m}>
                  {m}
                </option>
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
            <span class="settings-label">Local Server URL</span>
            <input
              class="settings-input"
              type="text"
              value={ml.local?.server_url || ""}
              onInput={(e) =>
                update(
                  "ml.local.server_url",
                  (e.target as HTMLInputElement).value
                )
              }
              placeholder="http://127.0.0.1:7774"
            />
          </div>
          <div class="settings-row">
            <div class="settings-label-group">
              <span class="settings-label">Cloud ML</span>
              <div class="settings-label-sub">
                Enabled automatically via Sigil Cloud (Pro/Team)
              </div>
            </div>
            <div class="toggle-switch">
              <Toggle
                label=""
                checked={ml.cloud?.enabled ?? false}
                onChange={(v) => update("ml.cloud.enabled", v)}
              />
            </div>
          </div>
        </div>
      </div>

      {/* Cloud */}
      <div class="settings-section">
        <h3>Sigil Cloud</h3>
        <div class="settings-section-desc">
          Sign in to access cloud AI, team sync, and ML predictions.
        </div>
        <div class="settings-card">
          <CloudSettings />
        </div>
      </div>

      {/* Data */}
      <div class="settings-section">
        <h3>Data</h3>
        <div class="settings-section-desc">
          How long raw events are retained and how often analysis runs.
        </div>
        <div class="settings-card">
          <div class="settings-row">
            <span class="settings-label">Raw Event Days</span>
            <input
              class="settings-input"
              type="number"
              value={retention.raw_event_days ?? 30}
              onInput={(e) =>
                update(
                  "retention.raw_event_days",
                  parseInt((e.target as HTMLInputElement).value, 10)
                )
              }
              min={1}
            />
          </div>
          <div class="settings-row">
            <span class="settings-label">Analyze Every</span>
            <input
              class="settings-input"
              type="text"
              value={daemon.analyze_every || ""}
              onInput={(e) =>
                update(
                  "daemon.analyze_every",
                  (e.target as HTMLInputElement).value
                )
              }
              placeholder="1h"
            />
          </div>
        </div>
      </div>

      {/* Fleet / Team Insights */}
      <div class="settings-section">
        <h3>Team Insights</h3>
        <div class="settings-section-desc">
          Anonymized aggregate metrics shared with your team. Requires a Team or
          Enterprise account. No raw data leaves your machine.
        </div>
        <div class="settings-card">
          <div class="settings-row">
            <div class="settings-label-group">
              <span class="settings-label">Enabled</span>
              <div class="settings-label-sub">
                Auto-configured when signed in with a Team account
              </div>
            </div>
            <div class="toggle-switch">
              <Toggle
                label=""
                checked={fleet.enabled ?? false}
                onChange={(v) => update("fleet.enabled", v)}
              />
            </div>
          </div>
        </div>
      </div>

      {/* Updates */}
      <div class="settings-section">
        <h3>Updates</h3>
        <div class="settings-section-desc">
          Control how Sigil checks for and installs updates.
        </div>
        <div class="settings-card">
          <div class="settings-row">
            <span class="settings-label">Current Version</span>
            <span class="settings-value-text">{appVersion || "unknown"}</span>
          </div>
          <div class="settings-row">
            <span class="settings-label">Update Mode</span>
            <select
              class="settings-select"
              value={daemon.update_mode || "notify"}
              onChange={(e) =>
                update(
                  "daemon.update_mode",
                  (e.target as HTMLSelectElement).value
                )
              }
            >
              <option value="auto">Auto</option>
              <option value="notify">Notify</option>
              <option value="disabled">Disabled</option>
            </select>
          </div>
          <div class="settings-row">
            <span class="settings-label">
              {updateCheckMsg || "Check for updates manually"}
            </span>
            <button
              class="btn daemon-btn"
              onClick={handleCheckUpdate}
              disabled={checkingUpdate}
            >
              {checkingUpdate ? "Checking..." : "Check Now"}
            </button>
          </div>
        </div>
      </div>

      {/* Save + Setup */}
      <div class="settings-save-area">
        <button
          class="btn save-btn"
          onClick={handleSave}
          disabled={!isDirty() || saving}
        >
          {saving ? "Saving..." : "Save Changes"}
        </button>
        {onRerunSetup && (
          <button class="btn daemon-btn" onClick={onRerunSetup}>
            Re-run Setup Wizard
          </button>
        )}
      </div>
    </div>
  );
}

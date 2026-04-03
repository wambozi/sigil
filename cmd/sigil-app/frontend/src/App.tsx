import { useState, useEffect, useCallback } from "preact/hooks";
import { StatusBar } from "./components/StatusBar";
import { UpdateBanner, UpdateInfoPayload } from "./components/UpdateBanner";
import { SuggestionList } from "./views/SuggestionList";
import { SuggestionDetail } from "./views/SuggestionDetail";
import { DaySummary } from "./views/DaySummary";
import { AskSigil } from "./views/AskSigil";
import { Plugins } from "./views/Plugins";
import { Settings } from "./views/Settings";
import { Analytics } from "./views/Analytics";
import { Timeline } from "./views/Timeline";
import { Wizard } from "./views/Wizard";

// Type stubs — Wails generates the real bindings at build time.
// These are resolved from ../wailsjs/ by the Wails runtime.
declare const window: Window & {
  go: {
    main: {
      App: {
        GetSuggestions(): Promise<any[]>;
        GetStatus(): Promise<any>;
        IsConnected(): Promise<boolean>;
        GetCurrentTask(): Promise<any>;
        CheckInit(): Promise<{ initialized: boolean; config_path: string }>;
        NotifyWindowFocus(): Promise<void>;
        NotifyWindowBlur(): Promise<void>;
      };
    };
  };
  runtime: {
    EventsOn(event: string, cb: (...args: any[]) => void): () => void;
    EventsEmit(event: string, ...args: any[]): void;
  };
};

type View = "list" | "detail" | "summary" | "timeline" | "ask" | "plugins" | "analytics" | "settings" | "wizard";
type Filter = "all" | "pending" | "accepted" | "dismissed";

export interface Suggestion {
  id: number;
  title: string;
  body: string;
  text?: string;
  confidence: number;
  category: string;
  action_cmd?: string;
  status: string;
  created_at?: string;
}

export function App() {
  const [view, setView] = useState<View>("list");
  const [suggestions, setSuggestions] = useState<Suggestion[]>([]);
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const [connected, setConnected] = useState(false);
  const [filter, setFilter] = useState<Filter>("all");
  const [currentTask, setCurrentTask] = useState<any>(null);
  const [updateInfo, setUpdateInfo] = useState<UpdateInfoPayload | null>(null);

  const fetchSuggestions = useCallback(async () => {
    try {
      const data = await window.go.main.App.GetSuggestions();
      if (Array.isArray(data)) {
        setSuggestions(data);
      }
    } catch {
      // Daemon unavailable.
    }
  }, []);

  const fetchTask = useCallback(async () => {
    try {
      const task = await window.go.main.App.GetCurrentTask();
      setCurrentTask(task);
    } catch {
      // Daemon unavailable.
    }
  }, []);

  useEffect(() => {
    // Check if daemon is initialized — show wizard if not.
    window.go.main.App.CheckInit()
      .then((result) => {
        if (!result.initialized) {
          setView("wizard");
        }
      })
      .catch(() => {
        // If daemon unreachable, still show normal UI (user may start daemon later).
      });

    // Initial data fetch — verify daemon is actually responsive, not just
    // that the socket file exists.
    window.go.main.App.GetStatus()
      .then(() => setConnected(true))
      .catch(() => setConnected(false));
    fetchSuggestions();
    fetchTask();

    // Listen for push events.
    const offSuggestion = window.runtime.EventsOn(
      "suggestion:new",
      (sg: Suggestion) => {
        setSuggestions((prev) => [sg, ...prev]);
      }
    );

    const offConnection = window.runtime.EventsOn(
      "connection:changed",
      (c: boolean) => {
        setConnected(c);
        if (c) {
          fetchSuggestions();
          fetchTask();
        }
      }
    );

    const offUpdate = window.runtime.EventsOn(
      "update:available",
      (info: UpdateInfoPayload) => {
        // Respect 24h snooze.
        try {
          const snoozedUntil = localStorage.getItem("sigil_update_snoozed");
          if (snoozedUntil && Date.now() < Number(snoozedUntil)) {
            return;
          }
        } catch {
          // localStorage unavailable.
        }
        setUpdateInfo(info);
      }
    );

    // Notify Go backend of window focus/blur so it can suppress native
    // notifications when the app window is visible.
    const onFocus = () => window.go.main.App.NotifyWindowFocus();
    const onBlur = () => window.go.main.App.NotifyWindowBlur();
    globalThis.addEventListener("focus", onFocus);
    globalThis.addEventListener("blur", onBlur);

    return () => {
      offSuggestion();
      offConnection();
      offUpdate();
      globalThis.removeEventListener("focus", onFocus);
      globalThis.removeEventListener("blur", onBlur);
    };
  }, [fetchSuggestions, fetchTask]);

  const handleSelect = (id: number) => {
    setSelectedId(id);
    setView("detail");
  };

  const handleBack = () => {
    setView("list");
    setSelectedId(null);
    fetchSuggestions();
  };

  const selectedSuggestion = suggestions.find((s) => s.id === selectedId);

  const filteredSuggestions =
    filter === "all"
      ? suggestions
      : suggestions.filter((s) => {
          if (filter === "pending") return s.status === "shown" || s.status === "pending";
          return s.status === filter;
        });

  const handleWizardComplete = () => {
    setView("list");
    fetchSuggestions();
    fetchTask();
  };

  // Show wizard fullscreen (no status bar or tab bar).
  if (view === "wizard") {
    return (
      <div class="app">
        <Wizard onComplete={handleWizardComplete} />
      </div>
    );
  }

  return (
    <div class="app">
      <StatusBar connected={connected} currentTask={currentTask} />

      {updateInfo && (
        <UpdateBanner
          info={updateInfo}
          onDismiss={() => setUpdateInfo(null)}
        />
      )}

      <main class="main-content">
        {view === "list" && (
          <SuggestionList
            suggestions={filteredSuggestions}
            allSuggestions={suggestions}
            filter={filter}
            onFilterChange={setFilter}
            onSelect={handleSelect}
          />
        )}
        {view === "detail" && selectedSuggestion && (
          <SuggestionDetail
            suggestion={selectedSuggestion}
            onBack={handleBack}
            onUpdate={fetchSuggestions}
          />
        )}
        {view === "summary" && <DaySummary onViewTimeline={() => setView("timeline")} />}
        {view === "timeline" && <Timeline />}
        {view === "ask" && <AskSigil />}
        {view === "plugins" && <Plugins />}
        {view === "analytics" && <Analytics />}
        {view === "settings" && <Settings onRerunSetup={() => setView("wizard")} connected={connected} />}
      </main>

      <nav class="tab-bar">
        <button
          class={`tab ${view === "list" ? "active" : ""}`}
          onClick={() => setView("list")}
        >
          Suggestions
        </button>
        <button
          class={`tab ${view === "summary" ? "active" : ""}`}
          onClick={() => setView("summary")}
        >
          Summary
        </button>
        <button
          class={`tab ${view === "ask" ? "active" : ""}`}
          onClick={() => setView("ask")}
        >
          Ask
        </button>
        <button
          class={`tab ${view === "plugins" ? "active" : ""}`}
          onClick={() => setView("plugins")}
        >
          Plugins
        </button>
        <button
          class={`tab ${view === "analytics" ? "active" : ""}`}
          onClick={() => setView("analytics")}
        >
          Analytics
        </button>
        <button
          class={`tab ${view === "settings" ? "active" : ""}`}
          onClick={() => setView("settings")}
        >
          &#9881; Settings
        </button>
      </nav>
    </div>
  );
}

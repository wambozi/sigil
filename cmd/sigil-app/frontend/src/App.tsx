import { useState, useEffect, useCallback } from "preact/hooks";
import { StatusBar } from "./components/StatusBar";
import { SuggestionList } from "./views/SuggestionList";
import { SuggestionDetail } from "./views/SuggestionDetail";
import { DaySummary } from "./views/DaySummary";
import { AskSigil } from "./views/AskSigil";

// Type stubs — Wails generates the real bindings at build time.
// These are resolved from ../wailsjs/ by the Wails runtime.
declare const window: Window & {
  go: {
    main: {
      App: {
        GetSuggestions(): Promise<any[]>;
        IsConnected(): Promise<boolean>;
        GetCurrentTask(): Promise<any>;
      };
    };
  };
  runtime: {
    EventsOn(event: string, cb: (...args: any[]) => void): () => void;
  };
};

type View = "list" | "detail" | "summary" | "ask";
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
    // Initial data fetch.
    window.go.main.App.IsConnected().then(setConnected).catch(() => {});
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

    return () => {
      offSuggestion();
      offConnection();
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

  return (
    <div class="app">
      <StatusBar connected={connected} currentTask={currentTask} />

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
        {view === "summary" && <DaySummary />}
        {view === "ask" && <AskSigil />}
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
      </nav>
    </div>
  );
}

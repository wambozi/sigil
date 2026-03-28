import { useState } from "preact/hooks";

declare const window: Window & {
  go: {
    main: {
      App: {
        Ask(query: string): Promise<any>;
      };
    };
  };
};

export function AskSigil() {
  const [query, setQuery] = useState("");
  const [response, setResponse] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const handleSubmit = async () => {
    const q = query.trim();
    if (!q || loading) return;

    setLoading(true);
    setError(null);
    setResponse(null);

    try {
      const result = await window.go.main.App.Ask(q);
      if (result && typeof result === "object") {
        setResponse(
          result.answer || result.response || JSON.stringify(result, null, 2)
        );
      } else {
        setResponse(String(result));
      }
    } catch {
      setError("Could not reach the daemon. Is it running?");
    } finally {
      setLoading(false);
    }
  };

  const handleKeyDown = (e: KeyboardEvent) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSubmit();
    }
  };

  return (
    <div class="ask-view">
      <div class="ask-input-group">
        <input
          class="ask-input"
          type="text"
          placeholder="Ask Sigil anything..."
          value={query}
          onInput={(e) => setQuery((e.target as HTMLInputElement).value)}
          onKeyDown={handleKeyDown}
        />
        <button
          class="btn btn-primary"
          onClick={handleSubmit}
          disabled={loading || !query.trim()}
        >
          {loading ? <span class="loading-spinner" /> : "Ask"}
        </button>
      </div>

      {error && (
        <div class="ask-response" style={{ color: "var(--danger)" }}>
          {error}
        </div>
      )}

      {response && <div class="ask-response">{response}</div>}

      {!response && !error && (
        <div class="empty-state">
          <div class="empty-state-icon">&#128172;</div>
          <div class="empty-state-title">Ask Sigil</div>
          <div class="empty-state-text">
            Ask questions about your workflow, codebase, or patterns Sigil has
            observed.
          </div>
        </div>
      )}
    </div>
  );
}

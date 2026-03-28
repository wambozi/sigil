import { useState } from "preact/hooks";
import type { Suggestion } from "../App";

declare const window: Window & {
  go: {
    main: {
      App: {
        AcceptSuggestion(id: number): Promise<void>;
        DismissSuggestion(id: number): Promise<void>;
      };
    };
  };
};

interface SuggestionDetailProps {
  suggestion: Suggestion;
  onBack: () => void;
  onUpdate: () => void;
}

function confidenceClass(confidence: number): string {
  if (confidence >= 0.7) return "high";
  if (confidence >= 0.4) return "medium";
  return "low";
}

export function SuggestionDetail({
  suggestion,
  onBack,
  onUpdate,
}: SuggestionDetailProps) {
  const [status, setStatus] = useState(suggestion.status);
  const pct = Math.round((suggestion.confidence || 0) * 100);
  const body = suggestion.body || suggestion.text || "";
  const isPending = status === "shown" || status === "pending";

  const handleAccept = async () => {
    try {
      await window.go.main.App.AcceptSuggestion(suggestion.id);
      setStatus("accepted");
      onUpdate();
    } catch {
      // Daemon unavailable.
    }
  };

  const handleDismiss = async () => {
    try {
      await window.go.main.App.DismissSuggestion(suggestion.id);
      setStatus("dismissed");
      onUpdate();
    } catch {
      // Daemon unavailable.
    }
  };

  return (
    <div class="detail-view">
      <div class="detail-header">
        <button class="back-btn" onClick={onBack}>
          &larr; Back
        </button>
      </div>

      <h2 class="detail-title">{suggestion.title}</h2>

      <div class="detail-meta">
        <div class="detail-meta-item">
          <span class="detail-meta-label">Confidence:</span>
          <span class={`confidence-badge ${confidenceClass(suggestion.confidence)}`}>
            {pct}%
          </span>
        </div>
        {suggestion.category && (
          <div class="detail-meta-item">
            <span class="detail-meta-label">Category:</span>
            <span>{suggestion.category}</span>
          </div>
        )}
        <div class="detail-meta-item">
          <span class="detail-meta-label">Status:</span>
          <span>{status}</span>
        </div>
        {suggestion.created_at && (
          <div class="detail-meta-item">
            <span class="detail-meta-label">Time:</span>
            <span>{new Date(suggestion.created_at).toLocaleString()}</span>
          </div>
        )}
      </div>

      <div class="detail-body">{body}</div>

      {suggestion.action_cmd && (
        <div class="detail-action-cmd">{suggestion.action_cmd}</div>
      )}

      {isPending && (
        <div class="detail-actions">
          <button class="btn btn-accept" onClick={handleAccept}>
            Accept
          </button>
          <button class="btn btn-dismiss" onClick={handleDismiss}>
            Dismiss
          </button>
        </div>
      )}
    </div>
  );
}

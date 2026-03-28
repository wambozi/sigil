import type { Suggestion } from "../App";

interface SuggestionCardProps {
  suggestion: Suggestion;
  onClick: () => void;
}

function statusIcon(status: string): string {
  switch (status) {
    case "accepted":
      return "\u2713"; // checkmark
    case "dismissed":
      return "\u2717"; // x mark
    default:
      return "\u25CF"; // filled circle (pending)
  }
}

function statusClass(status: string): string {
  switch (status) {
    case "accepted":
      return "accepted";
    case "dismissed":
      return "dismissed";
    default:
      return "pending";
  }
}

function confidenceClass(confidence: number): string {
  if (confidence >= 0.7) return "high";
  if (confidence >= 0.4) return "medium";
  return "low";
}

function relativeTime(dateStr?: string): string {
  if (!dateStr) return "";
  const date = new Date(dateStr);
  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  const diffMin = Math.floor(diffMs / 60000);

  if (diffMin < 1) return "just now";
  if (diffMin < 60) return `${diffMin}m ago`;
  const diffHr = Math.floor(diffMin / 60);
  if (diffHr < 24) return `${diffHr}h ago`;
  const diffDay = Math.floor(diffHr / 24);
  return `${diffDay}d ago`;
}

export function SuggestionCard({ suggestion, onClick }: SuggestionCardProps) {
  const pct = Math.round((suggestion.confidence || 0) * 100);

  return (
    <div class="suggestion-card" onClick={onClick}>
      <span
        class={`suggestion-status-icon ${statusClass(suggestion.status)}`}
      >
        {statusIcon(suggestion.status)}
      </span>
      <div class="suggestion-content">
        <div class="suggestion-title">{suggestion.title}</div>
        <div class="suggestion-meta">
          <span class={`confidence-badge ${confidenceClass(suggestion.confidence)}`}>
            {pct}%
          </span>
          {suggestion.category && <span>{suggestion.category}</span>}
          <span>{relativeTime(suggestion.created_at)}</span>
        </div>
      </div>
    </div>
  );
}

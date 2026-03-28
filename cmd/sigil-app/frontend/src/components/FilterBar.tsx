import type { Suggestion } from "../App";

type Filter = "all" | "pending" | "accepted" | "dismissed";

interface FilterBarProps {
  filter: Filter;
  onFilterChange: (f: Filter) => void;
  suggestions: Suggestion[];
}

export function FilterBar({
  filter,
  onFilterChange,
  suggestions,
}: FilterBarProps) {
  const counts = {
    all: suggestions.length,
    pending: suggestions.filter(
      (s) => s.status === "shown" || s.status === "pending"
    ).length,
    accepted: suggestions.filter((s) => s.status === "accepted").length,
    dismissed: suggestions.filter((s) => s.status === "dismissed").length,
  };

  const filters: { key: Filter; label: string }[] = [
    { key: "all", label: "All" },
    { key: "pending", label: "Pending" },
    { key: "accepted", label: "Accepted" },
    { key: "dismissed", label: "Dismissed" },
  ];

  return (
    <div class="filter-bar">
      {filters.map((f) => (
        <button
          key={f.key}
          class={`filter-btn ${filter === f.key ? "active" : ""}`}
          onClick={() => onFilterChange(f.key)}
        >
          {f.label}
          <span class="filter-count">{counts[f.key]}</span>
        </button>
      ))}
    </div>
  );
}

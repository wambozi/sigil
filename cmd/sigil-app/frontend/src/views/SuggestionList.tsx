import type { Suggestion } from "../App";
import { FilterBar } from "../components/FilterBar";
import { SuggestionCard } from "../components/SuggestionCard";

type Filter = "all" | "pending" | "accepted" | "dismissed";

interface SuggestionListProps {
  suggestions: Suggestion[];
  allSuggestions: Suggestion[];
  filter: Filter;
  onFilterChange: (f: Filter) => void;
  onSelect: (id: number) => void;
}

export function SuggestionList({
  suggestions,
  allSuggestions,
  filter,
  onFilterChange,
  onSelect,
}: SuggestionListProps) {
  return (
    <div>
      <FilterBar
        filter={filter}
        onFilterChange={onFilterChange}
        suggestions={allSuggestions}
      />
      {suggestions.length === 0 ? (
        <div class="empty-state">
          <div class="empty-state-icon">&#128161;</div>
          <div class="empty-state-title">No suggestions yet</div>
          <div class="empty-state-text">
            Sigil will surface suggestions as it learns your workflow.
          </div>
        </div>
      ) : (
        <div class="suggestion-list">
          {suggestions.map((sg) => (
            <SuggestionCard
              key={sg.id}
              suggestion={sg}
              onClick={() => onSelect(sg.id)}
            />
          ))}
        </div>
      )}
    </div>
  );
}

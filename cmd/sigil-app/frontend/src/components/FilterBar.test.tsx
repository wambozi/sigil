import { render, fireEvent } from '@testing-library/preact';
import { describe, it, expect, vi } from 'vitest';
import { FilterBar } from './FilterBar';
import type { Suggestion } from '../App';

const makeSuggestion = (overrides: Partial<Suggestion> = {}): Suggestion => ({
  id: 1,
  title: 'Test suggestion',
  body: 'Test body',
  confidence: 0.8,
  category: 'testing',
  status: 'shown',
  ...overrides,
});

describe('FilterBar', () => {
  const suggestions: Suggestion[] = [
    makeSuggestion({ id: 1, status: 'shown' }),
    makeSuggestion({ id: 2, status: 'pending' }),
    makeSuggestion({ id: 3, status: 'accepted' }),
    makeSuggestion({ id: 4, status: 'dismissed' }),
    makeSuggestion({ id: 5, status: 'accepted' }),
  ];

  it('renders all four filter buttons', () => {
    const onFilterChange = vi.fn();
    const { getByText } = render(
      <FilterBar filter="all" onFilterChange={onFilterChange} suggestions={suggestions} />
    );
    expect(getByText('All')).toBeTruthy();
    expect(getByText('Pending')).toBeTruthy();
    expect(getByText('Accepted')).toBeTruthy();
    expect(getByText('Dismissed')).toBeTruthy();
  });

  it('displays correct counts for each filter', () => {
    const onFilterChange = vi.fn();
    const { container } = render(
      <FilterBar filter="all" onFilterChange={onFilterChange} suggestions={suggestions} />
    );
    const counts = container.querySelectorAll('.filter-count');
    // all=5, pending=2 (shown+pending), accepted=2, dismissed=1
    expect(counts[0].textContent).toBe('5');
    expect(counts[1].textContent).toBe('2');
    expect(counts[2].textContent).toBe('2');
    expect(counts[3].textContent).toBe('1');
  });

  it('marks the active filter button', () => {
    const onFilterChange = vi.fn();
    const { container } = render(
      <FilterBar filter="accepted" onFilterChange={onFilterChange} suggestions={suggestions} />
    );
    const buttons = container.querySelectorAll('.filter-btn');
    // "Accepted" is the third button (index 2)
    expect(buttons[2].classList.contains('active')).toBe(true);
    // Others should not be active
    expect(buttons[0].classList.contains('active')).toBe(false);
    expect(buttons[1].classList.contains('active')).toBe(false);
    expect(buttons[3].classList.contains('active')).toBe(false);
  });

  it('calls onFilterChange when a filter button is clicked', () => {
    const onFilterChange = vi.fn();
    const { getByText } = render(
      <FilterBar filter="all" onFilterChange={onFilterChange} suggestions={suggestions} />
    );
    fireEvent.click(getByText('Dismissed'));
    expect(onFilterChange).toHaveBeenCalledWith('dismissed');
  });

  it('calls onFilterChange with "pending" when Pending is clicked', () => {
    const onFilterChange = vi.fn();
    const { getByText } = render(
      <FilterBar filter="all" onFilterChange={onFilterChange} suggestions={suggestions} />
    );
    fireEvent.click(getByText('Pending'));
    expect(onFilterChange).toHaveBeenCalledWith('pending');
  });
});

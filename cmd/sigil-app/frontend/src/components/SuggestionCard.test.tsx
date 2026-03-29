import { render, fireEvent } from '@testing-library/preact';
import { describe, it, expect, vi } from 'vitest';
import { SuggestionCard } from './SuggestionCard';
import type { Suggestion } from '../App';

const makeSuggestion = (overrides: Partial<Suggestion> = {}): Suggestion => ({
  id: 1,
  title: 'Refactor database layer',
  body: 'Consider splitting the monolithic query file.',
  confidence: 0.85,
  category: 'refactoring',
  status: 'shown',
  created_at: new Date().toISOString(),
  ...overrides,
});

describe('SuggestionCard', () => {
  it('renders the suggestion title', () => {
    const onClick = vi.fn();
    const { getByText } = render(
      <SuggestionCard suggestion={makeSuggestion()} onClick={onClick} />
    );
    expect(getByText('Refactor database layer')).toBeTruthy();
  });

  it('displays confidence as a percentage', () => {
    const onClick = vi.fn();
    const { getByText } = render(
      <SuggestionCard suggestion={makeSuggestion({ confidence: 0.72 })} onClick={onClick} />
    );
    expect(getByText('72%')).toBeTruthy();
  });

  it('renders the category', () => {
    const onClick = vi.fn();
    const { getByText } = render(
      <SuggestionCard suggestion={makeSuggestion({ category: 'performance' })} onClick={onClick} />
    );
    expect(getByText('performance')).toBeTruthy();
  });

  it('shows checkmark icon for accepted status', () => {
    const onClick = vi.fn();
    const { container } = render(
      <SuggestionCard suggestion={makeSuggestion({ status: 'accepted' })} onClick={onClick} />
    );
    const icon = container.querySelector('.suggestion-status-icon');
    expect(icon?.textContent).toBe('\u2713');
    expect(icon?.classList.contains('accepted')).toBe(true);
  });

  it('shows x-mark icon for dismissed status', () => {
    const onClick = vi.fn();
    const { container } = render(
      <SuggestionCard suggestion={makeSuggestion({ status: 'dismissed' })} onClick={onClick} />
    );
    const icon = container.querySelector('.suggestion-status-icon');
    expect(icon?.textContent).toBe('\u2717');
    expect(icon?.classList.contains('dismissed')).toBe(true);
  });

  it('shows filled circle icon for pending status', () => {
    const onClick = vi.fn();
    const { container } = render(
      <SuggestionCard suggestion={makeSuggestion({ status: 'shown' })} onClick={onClick} />
    );
    const icon = container.querySelector('.suggestion-status-icon');
    expect(icon?.textContent).toBe('\u25CF');
    expect(icon?.classList.contains('pending')).toBe(true);
  });

  it('applies high confidence class for >= 0.7', () => {
    const onClick = vi.fn();
    const { container } = render(
      <SuggestionCard suggestion={makeSuggestion({ confidence: 0.85 })} onClick={onClick} />
    );
    const badge = container.querySelector('.confidence-badge');
    expect(badge?.classList.contains('high')).toBe(true);
  });

  it('applies medium confidence class for >= 0.4 and < 0.7', () => {
    const onClick = vi.fn();
    const { container } = render(
      <SuggestionCard suggestion={makeSuggestion({ confidence: 0.5 })} onClick={onClick} />
    );
    const badge = container.querySelector('.confidence-badge');
    expect(badge?.classList.contains('medium')).toBe(true);
  });

  it('applies low confidence class for < 0.4', () => {
    const onClick = vi.fn();
    const { container } = render(
      <SuggestionCard suggestion={makeSuggestion({ confidence: 0.2 })} onClick={onClick} />
    );
    const badge = container.querySelector('.confidence-badge');
    expect(badge?.classList.contains('low')).toBe(true);
  });

  it('calls onClick when the card is clicked', () => {
    const onClick = vi.fn();
    const { container } = render(
      <SuggestionCard suggestion={makeSuggestion()} onClick={onClick} />
    );
    fireEvent.click(container.querySelector('.suggestion-card')!);
    expect(onClick).toHaveBeenCalledTimes(1);
  });
});

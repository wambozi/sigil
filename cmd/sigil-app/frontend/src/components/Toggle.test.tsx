import { render, fireEvent } from '@testing-library/preact';
import { describe, it, expect, vi } from 'vitest';
import { Toggle } from './Toggle';

describe('Toggle', () => {
  it('renders without a label', () => {
    const onChange = vi.fn();
    const { container } = render(<Toggle checked={false} onChange={onChange} />);
    expect(container.querySelector('.toggle-switch')).toBeTruthy();
    expect(container.querySelector('.toggle-label')).toBeNull();
  });

  it('renders with a label', () => {
    const onChange = vi.fn();
    const { getByText } = render(
      <Toggle checked={false} onChange={onChange} label="Enable notifications" />
    );
    expect(getByText('Enable notifications')).toBeTruthy();
  });

  it('shows active class when checked', () => {
    const onChange = vi.fn();
    const { container } = render(<Toggle checked={true} onChange={onChange} />);
    const track = container.querySelector('.toggle-track');
    expect(track?.classList.contains('active')).toBe(true);
  });

  it('does not show active class when unchecked', () => {
    const onChange = vi.fn();
    const { container } = render(<Toggle checked={false} onChange={onChange} />);
    const track = container.querySelector('.toggle-track');
    expect(track?.classList.contains('active')).toBe(false);
  });

  it('calls onChange with true when clicking an unchecked toggle', () => {
    const onChange = vi.fn();
    const { container } = render(<Toggle checked={false} onChange={onChange} />);
    const switchEl = container.querySelector('.toggle-switch')!;
    fireEvent.click(switchEl);
    expect(onChange).toHaveBeenCalledWith(true);
  });

  it('calls onChange with false when clicking a checked toggle', () => {
    const onChange = vi.fn();
    const { container } = render(<Toggle checked={true} onChange={onChange} />);
    const switchEl = container.querySelector('.toggle-switch')!;
    fireEvent.click(switchEl);
    expect(onChange).toHaveBeenCalledWith(false);
  });
});

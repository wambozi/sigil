import { render, fireEvent } from '@testing-library/preact';
import { describe, it, expect, vi } from 'vitest';
import { EditableList } from './EditableList';

describe('EditableList', () => {
  it('renders all items', () => {
    const onChange = vi.fn();
    const { getByText } = render(
      <EditableList items={['alpha', 'beta', 'gamma']} onChange={onChange} />
    );
    expect(getByText('alpha')).toBeTruthy();
    expect(getByText('beta')).toBeTruthy();
    expect(getByText('gamma')).toBeTruthy();
  });

  it('renders the default placeholder', () => {
    const onChange = vi.fn();
    const { container } = render(
      <EditableList items={[]} onChange={onChange} />
    );
    const input = container.querySelector('input') as HTMLInputElement;
    expect(input.placeholder).toBe('Add item...');
  });

  it('renders a custom placeholder', () => {
    const onChange = vi.fn();
    const { container } = render(
      <EditableList items={[]} onChange={onChange} placeholder="Add a path..." />
    );
    const input = container.querySelector('input') as HTMLInputElement;
    expect(input.placeholder).toBe('Add a path...');
  });

  it('adds a new item when the add button is clicked', () => {
    const onChange = vi.fn();
    const { container } = render(
      <EditableList items={['existing']} onChange={onChange} />
    );
    const input = container.querySelector('input') as HTMLInputElement;
    fireEvent.input(input, { target: { value: 'new-item' } });
    const addBtn = container.querySelector('.add-btn')!;
    fireEvent.click(addBtn);
    expect(onChange).toHaveBeenCalledWith(['existing', 'new-item']);
  });

  it('adds a new item when Enter is pressed', () => {
    const onChange = vi.fn();
    const { container } = render(
      <EditableList items={[]} onChange={onChange} />
    );
    const input = container.querySelector('input') as HTMLInputElement;
    fireEvent.input(input, { target: { value: 'enter-item' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(onChange).toHaveBeenCalledWith(['enter-item']);
  });

  it('does not add duplicate items', () => {
    const onChange = vi.fn();
    const { container } = render(
      <EditableList items={['dup']} onChange={onChange} />
    );
    const input = container.querySelector('input') as HTMLInputElement;
    fireEvent.input(input, { target: { value: 'dup' } });
    const addBtn = container.querySelector('.add-btn')!;
    fireEvent.click(addBtn);
    expect(onChange).not.toHaveBeenCalled();
  });

  it('does not add empty or whitespace-only items', () => {
    const onChange = vi.fn();
    const { container } = render(
      <EditableList items={[]} onChange={onChange} />
    );
    const input = container.querySelector('input') as HTMLInputElement;
    fireEvent.input(input, { target: { value: '   ' } });
    const addBtn = container.querySelector('.add-btn')!;
    fireEvent.click(addBtn);
    expect(onChange).not.toHaveBeenCalled();
  });

  it('removes an item when the remove button is clicked', () => {
    const onChange = vi.fn();
    const { container } = render(
      <EditableList items={['keep', 'remove-me', 'also-keep']} onChange={onChange} />
    );
    const removeBtns = container.querySelectorAll('.remove-btn');
    // Click remove on the second item (index 1)
    fireEvent.click(removeBtns[1]);
    expect(onChange).toHaveBeenCalledWith(['keep', 'also-keep']);
  });

  it('renders a remove button for each item', () => {
    const onChange = vi.fn();
    const { container } = render(
      <EditableList items={['a', 'b', 'c']} onChange={onChange} />
    );
    const removeBtns = container.querySelectorAll('.remove-btn');
    expect(removeBtns.length).toBe(3);
  });
});

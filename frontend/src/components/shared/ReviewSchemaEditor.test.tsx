import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { ReviewSchemaEditor } from './ReviewSchemaEditor';

describe('saving a schema inside the create-review form', () => {
  it.each(['click', 'enter'])('saves the template without creating a review via %s', async (method) => {
    const user = userEvent.setup();
    const save = vi.fn().mockResolvedValue({ id: 'template' });
    const create = vi.fn();
    const { container } = render(
      <form onSubmit={(event) => { event.preventDefault(); create(); }}>
        <ReviewSchemaEditor fields={[{ name: 'Notice', type: 'duration' }]} onChange={vi.fn()} onSaveTemplate={save} styles={{}} />
        <button type="submit">Create review</button>
      </form>,
    );
    await user.click(screen.getByRole('button', { name: 'Save as template' }));
    await user.type(screen.getByLabelText('Template name'), 'Notice template');
    if (method === 'enter') await user.keyboard('{Enter}');
    else await user.click(screen.getByRole('button', { name: /^Save$/ }));
    await waitFor(() => expect(save).toHaveBeenCalledWith('Notice template', false));
    expect(create).not.toHaveBeenCalled();
    expect(container.querySelectorAll('form')).toHaveLength(1);
    await user.click(screen.getByRole('button', { name: 'Create review' }));
    expect(create).toHaveBeenCalledTimes(1);
  });
});

import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { GoogleSignIn } from './GoogleSignIn';
import { api } from '../../api';
import { loadScript } from '../../utils/loadScript';
import { useAuthStore } from '../../stores/authStore';

vi.mock('../../api', () => ({ api: { request: vi.fn(), setToken: vi.fn() } }));
vi.mock('../../utils/loadScript', () => ({ loadScript: vi.fn().mockResolvedValue(undefined) }));

const config = { client_id: 'google-client', nonce: 'browser-bound-nonce', hosted_domain: 'firm.example' };
const initialize = vi.fn();
const renderButton = vi.fn((target: HTMLElement) => { target.textContent = 'Sign in with Google'; });
const callback = () => initialize.mock.calls[initialize.mock.calls.length - 1][0].callback as (value: { credential: string }) => Promise<void>;

beforeEach(() => {
  vi.clearAllMocks();
  useAuthStore.setState(useAuthStore.getInitialState(), true);
  vi.stubGlobal('google', { accounts: { id: { initialize, renderButton } } });
});

describe('Google sign-in', () => {
  it('only loads Google when the server enables it', async () => {
    vi.mocked(api.request).mockRejectedValue(new Error('Google is not configured'));
    render(<GoogleSignIn open />);
    await waitFor(() => expect(api.request).toHaveBeenCalledWith('/auth/google/config'));
    expect(loadScript).not.toHaveBeenCalled();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('passes the server nonce and sends the signed credential to the same-origin API', async () => {
    const user = { id: 'local-user', email: 'user@firm.example', roles: ['attorney'] };
    vi.mocked(api.request).mockResolvedValueOnce(config).mockResolvedValueOnce({ access_token: 'session', user });
    render(<GoogleSignIn open />);
    await waitFor(() => expect(initialize).toHaveBeenCalled());
    expect(initialize.mock.calls[0][0]).toMatchObject({ client_id: config.client_id, nonce: config.nonce, hd: config.hosted_domain, auto_select: false });
    await act(() => callback()({ credential: 'signed-google-token' }));
    expect(api.request).toHaveBeenLastCalledWith('/auth/google/login', { method: 'POST', body: JSON.stringify({ token: 'signed-google-token' }) });
    expect(api.setToken).toHaveBeenCalledWith('session');
    expect(useAuthStore.getState()).toMatchObject({ user, isAuthenticated: true });
  });

  it('requires the existing MFA step before establishing a session', async () => {
    vi.mocked(api.request).mockResolvedValueOnce(config).mockResolvedValueOnce({ mfa_required: true, mfa_token: 'challenge' });
    render(<GoogleSignIn open />);
    await waitFor(() => expect(initialize).toHaveBeenCalled());
    await act(() => callback()({ credential: 'signed-token' }));
    expect(api.setToken).not.toHaveBeenCalled();
    expect(useAuthStore.getState()).toMatchObject({ isAuthenticated: false, showMfaModal: true, mfaToken: 'challenge' });
  });

  it('gets a new nonce after a rejected sign-in and ignores duplicate callbacks', async () => {
    vi.mocked(api.request).mockResolvedValueOnce(config).mockRejectedValueOnce(new Error('Sign-in state expired')).mockResolvedValueOnce({ ...config, nonce: 'fresh-nonce' });
    render(<GoogleSignIn open />);
    await waitFor(() => expect(initialize).toHaveBeenCalled());
    const complete = callback();
    await act(() => complete({ credential: 'old-token' }));
    await act(() => complete({ credential: 'old-token' }));
    expect(api.request).toHaveBeenCalledTimes(2);
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    await waitFor(() => expect(initialize).toHaveBeenCalledTimes(2));
    expect(initialize.mock.calls[1][0].nonce).toBe('fresh-nonce');
  });

  it('links an authenticated account without replacing its current session', async () => {
    vi.mocked(api.request).mockResolvedValueOnce(config).mockResolvedValueOnce({ status: 'linked' });
    render(<GoogleSignIn open link />);
    await waitFor(() => expect(initialize).toHaveBeenCalled());
    expect(screen.getByText(/Link your Google Workspace account/)).toBeInTheDocument();
    await act(() => callback()({ credential: 'signed-token' }));
    expect(api.request).toHaveBeenLastCalledWith('/auth/google/link', expect.anything());
    expect(api.setToken).not.toHaveBeenCalled();
    expect(screen.getByRole('status')).toHaveTextContent('linked to your account');
  });

  it('ignores a popup response after the sign-in dialog closes', async () => {
    vi.mocked(api.request).mockResolvedValueOnce(config);
    const view = render(<GoogleSignIn open />);
    await waitFor(() => expect(initialize).toHaveBeenCalled());
    const complete = callback();
    view.unmount();
    await act(() => complete({ credential: 'late-token' }));
    expect(api.request).toHaveBeenCalledTimes(1);
    expect(api.setToken).not.toHaveBeenCalled();
  });
});

import { useEffect, useRef, useState } from 'react';
import { api } from '../../api';
import { useAuthStore } from '../../stores/authStore';
import { useUIStore } from '../../stores/uiStore';
import { loadScript } from '../../utils/loadScript';
import type { AuthResponse } from '../../api/types';

/** Google's rendered button supplies its accessible, approved brand treatment. */
export function GoogleSignIn({ open, link = false }: { open: boolean; link?: boolean }) {
  const button = useRef<HTMLDivElement>(null);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const [linked, setLinked] = useState(false);
  const [available, setAvailable] = useState(false);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    if (!open || linked) return;
    let active = true;
    let busy = false;
    const target = button.current;
    const initialize = async () => {
      let config: { client_id: string; nonce: string; hosted_domain: string; linked?: boolean };
      try { config = await api.request(link ? '/auth/google/link/config' : '/auth/google/config'); }
      catch { return; } // Disabled providers do not clutter the sign-in screen.
      if (!active) return;
      setAvailable(true);
      if (link && config.linked) { setLinked(true); return; }
      try {
        await loadScript('https://accounts.google.com/gsi/client');
        if (!active || !target) return;
        const identity = window.google?.accounts?.id;
        if (!identity) throw new Error('Google sign-in could not load. Please try again.');
        identity.initialize({
          client_id: config.client_id, nonce: config.nonce, hd: config.hosted_domain,
          auto_select: false, ux_mode: 'popup',
          callback: async ({ credential }: { credential: string }) => {
            if (!active || busy) return;
            busy = true;
            setLoading(true);
            setError('');
            try {
              const result: AuthResponse = await api.request(link ? '/auth/google/link' : '/auth/google/login', {
                method: 'POST', body: JSON.stringify({ token: credential }),
              });
              if (!active) return;
              if (link) { setLinked(true); return; }
              if (result.mfa_required && result.mfa_token) {
                useAuthStore.setState({ showLoginModal: false, showMfaModal: true, mfaToken: result.mfa_token, mfaCode: '', mfaError: '', loginPassword: '' });
              } else {
                if (!result.access_token || !result.user) throw new Error('Google sign-in did not create a session. Please try again.');
                api.setToken(result.access_token);
                useAuthStore.setState({ user: result.user, isAuthenticated: true, showLoginModal: false, loginEmail: '', loginPassword: '', loginError: '' });
                useUIStore.getState().addToast('Signed in successfully', 'success');
              }
            } catch (reason: unknown) {
              if (active) setError(reason instanceof Error ? reason.message : 'Google sign-in failed. Please try again.');
            } finally { if (active) setLoading(false); }
          },
        });
        identity.renderButton(target, { type: 'standard', theme: 'outline', size: 'large', text: 'signin_with', shape: 'rectangular' });
      } catch (reason: unknown) {
        if (active) setError(reason instanceof Error ? reason.message : 'Google sign-in failed to load.');
      }
    };
    void initialize();
    return () => { active = false; target?.replaceChildren(); };
  }, [open, attempt, link, linked]);

  if (!open) return null;
  return <div aria-busy={loading}>
    {link && available && !linked && <p>Link your Google Workspace account to sign in with Google. Use the Google account with your CaseCite email address.</p>}
    {linked ? <p role="status">Google sign-in is linked to your account.</p> : <div ref={button} hidden={loading} />}
    {loading && <p role="status">Completing Google sign-in…</p>}
    {error && <p role="alert">{error} <button type="button" onClick={() => { setError(''); setAttempt(value => value + 1); }}>Try again</button></p>}
  </div>;
}

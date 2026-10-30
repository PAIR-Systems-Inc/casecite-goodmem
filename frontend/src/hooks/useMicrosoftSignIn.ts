import { useEffect, useState } from 'react';
import { api } from '../api';
import type { MicrosoftAuthConfig } from '../api/types';
import { useAuthStore } from '../stores/authStore';
import { useUIStore } from '../stores/uiStore';

/** Microsoft sign-in uses the same account gates and MFA screen as passwords. */
export function useMicrosoftSignIn(open: boolean) {
  const [config, setConfig] = useState<MicrosoftAuthConfig | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  useEffect(() => {
    if (!open) return;
    let active = true;
    api.getMicrosoftAuthConfig().then(value => { if (active) setConfig(value); })
      .catch(() => { if (active) setConfig(null); });
    return () => { active = false; };
  }, [open]);

  const signIn = async () => {
    if (!config || loading) return;
    setLoading(true);
    setError('');
    try {
      const { PublicClientApplication } = await import('@azure/msal-browser');
      const client = new PublicClientApplication({
        auth: { clientId: config.client_id, authority: config.authority, redirectUri: config.redirect_uri },
        cache: { cacheLocation: 'memoryStorage' },
      });
      await client.initialize();
      const response = await client.loginPopup({ scopes: config.scopes, nonce: config.nonce, prompt: 'select_account' });
      const result = await api.loginWithMicrosoft(response.idToken);
      if (result.mfa_required && result.mfa_token) {
        useAuthStore.setState({ showLoginModal: false, showMfaModal: true, mfaToken: result.mfa_token, mfaCode: '', mfaError: '', loginPassword: '' });
      } else {
        useAuthStore.setState({ user: result.user, isAuthenticated: true, showLoginModal: false, loginEmail: '', loginPassword: '', loginError: '' });
        useUIStore.getState().addToast('Signed in successfully', 'success');
      }
    } catch (reason: unknown) {
      setError(reason instanceof Error ? reason.message : 'Microsoft sign-in failed. Please try again.');
      // Every attempt gets a fresh, browser-bound nonce, including cancelled popups.
      try { setConfig(await api.getMicrosoftAuthConfig()); } catch { setConfig(null); }
    } finally {
      setLoading(false);
    }
  };
  return { available: !!config, loading, error, signIn };
}

import { useState, type FormEvent } from 'react';
import { Scale, ArrowRight } from 'lucide-react';
import { api, errorMessage, type User } from './api';
import { Button } from './ui';

export function Login({ setup, onLogin }: { setup: boolean; onLogin: (u: User) => void }) {
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const data = Object.fromEntries(new FormData(e.currentTarget));
    setBusy(true);
    setError('');
    try {
      const r = await api<{ user: User }>(setup ? '/auth/register' : '/auth/login', 'POST', data);
      onLogin(r.user);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="login">
      <div className="login-story">
        <div className="brand">
          <Scale /> CaseCite <span>GOODMEM</span>
        </div>
        <h1>
          Your evidence.
          <br />A clearer argument.
        </h1>
        <p>Research, review, and draft with your documents in view.</p>
        <div className="story-rule" />
        <small>A workspace for careful legal work.</small>
      </div>
      <div className="auth-card">
        <p className="eyebrow">{setup ? 'NEW WORKSPACE' : 'WELCOME BACK'}</p>
        <h2>{setup ? 'Create the administrator account' : 'Sign in to CaseCite'}</h2>
        <p>
          {setup
            ? 'Use the installation setup token from your server configuration.'
            : 'Use the account provided by your workspace administrator.'}
        </p>
        <form onSubmit={submit}>
          {setup && (
            <label>
              Name
              <input name="name" required autoComplete="name" />
            </label>
          )}
          <label>
            Email
            <input name="email" type="email" required autoComplete="username" />
          </label>
          <label>
            Password
            <input
              name="password"
              type="password"
              required
              minLength={setup ? 12 : undefined}
              autoComplete={setup ? 'new-password' : 'current-password'}
            />
          </label>
          {setup && (
            <label>
              Installation setup token
              <input name="bootstrap_token" type="password" required autoComplete="off" />
            </label>
          )}
          {error && (
            <p className="error" role="alert">
              {error}
            </p>
          )}
          <Button className="primary" busy={busy}>
            {setup ? 'Create workspace' : 'Sign in'}
            <ArrowRight size={16} />
          </Button>
        </form>
      </div>
    </div>
  );
}

export function PasswordForm({ onDone }: { onDone: () => void }) {
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  return (
    <form
      onSubmit={async (e) => {
        e.preventDefault();
        const form = e.currentTarget;
        setBusy(true);
        try {
          await api('/auth/change-password', 'POST', Object.fromEntries(new FormData(form)));
          form.reset();
          onDone();
        } catch (e) {
          setError(errorMessage(e));
        } finally {
          setBusy(false);
        }
      }}
    >
      <label>
        Current password
        <input type="password" name="current_password" autoComplete="current-password" required />
      </label>
      <label>
        New password
        <input type="password" name="new_password" autoComplete="new-password" minLength={12} maxLength={72} required />
      </label>
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      <Button className="primary" busy={busy}>
        Change password
      </Button>
    </form>
  );
}

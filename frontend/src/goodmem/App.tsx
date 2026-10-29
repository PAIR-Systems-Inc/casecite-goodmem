import { useEffect, useState } from 'react';
import { Scale } from 'lucide-react';
import { api, errorMessage, type User } from './api';
import { Login, PasswordForm } from './Auth';
import { Workspace } from './Workspace';
import './app.css';

export function GoodMemApp() {
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);
  const [setup, setSetup] = useState(false);
  const [error, setError] = useState('');
  useEffect(() => {
    Promise.all([
      api<{ setup_required: boolean }>('/config').then((c) => setSetup(c.setup_required)),
      api<User>('/auth/me')
        .then(setUser)
        .catch(() => {}),
    ])
      .catch((e) => setError(errorMessage(e)))
      .finally(() => setLoading(false));
  }, []);
  if (loading)
    return (
      <div className="splash">
        <Scale size={34} />
        <p>Opening CaseCite…</p>
      </div>
    );
  if (error)
    return (
      <div className="splash">
        <p role="alert">{error}</p>
        <button onClick={() => window.location.reload()}>Try again</button>
      </div>
    );
  if (!user) return <Login setup={setup} onLogin={setUser} />;
  if (user.must_change_password)
    return (
      <div className="login">
        <div className="auth-card">
          <h1>Choose your password</h1>
          <p>Replace your temporary password to open your workspace.</p>
          <PasswordForm onDone={() => setUser({ ...user, must_change_password: false })} />
        </div>
      </div>
    );
  return (
    <Workspace
      user={user}
      logout={() => {
        setUser(null);
        setSetup(false);
      }}
    />
  );
}

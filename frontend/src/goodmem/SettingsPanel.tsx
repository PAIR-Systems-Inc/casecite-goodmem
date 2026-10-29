import { useEffect, useState } from 'react';
import { api, type User, type Settings } from './api';
import { Button } from './ui';
import { PasswordForm } from './Auth';

export function SettingsPanel({ user, report }: { user: User; report: (e: unknown) => void }) {
  const [settings, setSettings] = useState<Settings | null>(null);
  const [message, setMessage] = useState('');
  const [users, setUsers] = useState<{ id: string; name: string; email: string; active: boolean; role: string }[]>([]);
  const [audit, setAudit] = useState<{ id: number; event_type: string; created_at: string; resource_type: string }[]>(
    [],
  );
  const [sessions, setSessions] = useState<{ created_at: string; last_activity: string; ip_address: string }[]>([]);
  const load = () => api<Settings>('/settings').then(setSettings);
  useEffect(() => {
    load().catch(report);
    api<{ sessions: typeof sessions }>('/auth/sessions')
      .then((r) => setSessions(r.sessions))
      .catch(report);
    if (user.roles.includes('admin')) {
      api<{ users: typeof users }>('/users')
        .then((r) => setUsers(r.users))
        .catch(report);
    }
  }, []);
  return (
    <div className="settings-grid">
      {message && (
        <p className="notice" role="status">
          {message}
        </p>
      )}
      <section className="card settings-card">
        <h2>Model & retrieval</h2>
        {settings && (
          <form
            onSubmit={async (e) => {
              e.preventDefault();
              try {
                await api('/settings', 'PUT', settings);
                setMessage('Settings saved.');
              } catch (e) {
                report(e);
              }
            }}
          >
            <label>
              Model name
              <input
                value={settings.llm_model}
                onChange={(e) => setSettings({ ...settings, llm_model: e.target.value })}
                required
              />
            </label>
            <label>
              Passages per search
              <input
                type="number"
                min={1}
                max={20}
                value={settings.top_k}
                onChange={(e) => setSettings({ ...settings, top_k: Number(e.target.value) })}
              />
            </label>
            <Button className="primary">Save settings</Button>
            <p className="muted">
              Your administrator configures the provider endpoint. Upload limit: {settings.max_upload_mb} MB.
            </p>
          </form>
        )}
      </section>
      <section className="card settings-card">
        <h2>Provider keys</h2>
        <p>Personal keys override workspace defaults. Stored keys are encrypted and never returned to the browser.</p>
        {['openai', 'courtlistener'].map((provider) => (
          <form
            key={provider}
            onSubmit={async (e) => {
              e.preventDefault();
              const form = e.currentTarget;
              try {
                await api(`/keys/${provider}`, 'PUT', Object.fromEntries(new FormData(form)));
                form.reset();
                await load();
                setMessage('Key saved.');
              } catch (e) {
                report(e);
              }
            }}
          >
            <label>
              {provider === 'openai' ? 'OpenAI-compatible provider' : 'CourtListener'}
              <span className="badge">{settings?.keys[provider] ? 'Configured' : 'Not configured'}</span>
              <input name="api_key" type="password" autoComplete="off" placeholder="Enter a new key" required />
            </label>
            <div className="row-actions">
              <button>Save key</button>
              <button
                type="button"
                onClick={async () => {
                  try {
                    await api(`/keys/${provider}`, 'DELETE');
                    await load();
                    setMessage('Personal key removed. Workspace default applies if configured.');
                  } catch (e) {
                    report(e);
                  }
                }}
              >
                Remove personal key
              </button>
            </div>
          </form>
        ))}
      </section>
      <section className="card settings-card">
        <h2>Change password</h2>
        <PasswordForm onDone={() => setMessage('Password changed. Other sessions were signed out.')} />
      </section>
      <section className="card settings-card">
        <h2>Sessions</h2>
        {sessions.map((s, i) => (
          <p key={i}>
            {s.ip_address || 'Unknown address'}
            <small>Last active {new Date(s.last_activity).toLocaleString()}</small>
          </p>
        ))}
        <button
          onClick={async () => {
            try {
              await api('/auth/logout/all', 'POST');
              window.location.reload();
            } catch (e) {
              report(e);
            }
          }}
        >
          Sign out all sessions
        </button>
      </section>
      {user.roles.includes('admin') && (
        <>
          <section className="card settings-card">
            <h2>Workspace accounts</h2>
            <p>
              Create an account and share its temporary password securely. The recipient must change it before accessing
              documents.
            </p>
            <form
              onSubmit={async (e) => {
                e.preventDefault();
                const form = e.currentTarget;
                try {
                  await api('/users', 'POST', Object.fromEntries(new FormData(form)));
                  form.reset();
                  setUsers((await api<{ users: typeof users }>('/users')).users);
                  setMessage('Account created.');
                } catch (e) {
                  report(e);
                }
              }}
            >
              <label>
                Name
                <input name="name" required />
              </label>
              <label>
                Email
                <input name="email" type="email" required />
              </label>
              <label>
                Temporary password
                <input
                  name="password"
                  type="password"
                  minLength={12}
                  maxLength={72}
                  required
                  autoComplete="new-password"
                />
              </label>
              <Button className="primary">Create account</Button>
            </form>
            <div className="member-list">
              {users.map((u) => (
                <div key={u.id}>
                  <span>
                    <strong>{u.name}</strong>
                    <small>{u.email}</small>
                  </span>
                  {u.id !== user.id && (
                    <button
                      onClick={async () => {
                        try {
                          await api(`/users/${u.id}`, 'PATCH', { active: !u.active });
                          setUsers((await api<{ users: typeof users }>('/users')).users);
                        } catch (e) {
                          report(e);
                        }
                      }}
                    >
                      {u.active ? 'Disable' : 'Enable'}
                    </button>
                  )}
                </div>
              ))}
            </div>
          </section>
          <section className="card settings-card">
            <h2>Audit trail</h2>
            <p>Recent account, membership, document, and review actions.</p>
            <button
              onClick={async () => {
                try {
                  setAudit((await api<{ events: typeof audit }>('/audit')).events);
                } catch (e) {
                  report(e);
                }
              }}
            >
              Load recent events
            </button>
            <div className="audit-list">
              {audit.map((a) => (
                <p key={a.id}>
                  <strong>{a.event_type}</strong>
                  <small>
                    {new Date(a.created_at).toLocaleString()} · {a.resource_type}
                  </small>
                </p>
              ))}
            </div>
          </section>
        </>
      )}
    </div>
  );
}

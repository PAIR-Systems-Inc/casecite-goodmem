import { useEffect, useState } from 'react';
import { api, errorMessage, type Matter } from './api';
import { Button, Modal } from './ui';

export function MemberModal({ matter, close, onDelete }: { matter: Matter; close: () => void; onDelete: () => void }) {
  const [members, setMembers] = useState<{ user_id: string; name: string; email: string; is_owner: boolean }[]>([]);
  const [error, setError] = useState('');
  const load = () =>
    api<{ members: typeof members }>(`/matters/${matter.id}/members`).then((r) => setMembers(r.members));
  useEffect(() => {
    load().catch((e) => setError(errorMessage(e)));
  }, [matter.id]);
  return (
    <Modal title={matter.name} close={close}>
      <p>Members can read documents and saved work assigned to this matter. Item owners control edits and deletion.</p>
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      <div className="member-list">
        {members.map((m) => (
          <div key={m.user_id}>
            <span>
              <strong>{m.name}</strong>
              <small>{m.email}</small>
            </span>
            {m.is_owner ? (
              <span className="badge">Owner</span>
            ) : (
              matter.is_owner && (
                <button
                  onClick={async () => {
                    try {
                      await api(`/matters/${matter.id}/members/${m.user_id}`, 'DELETE');
                      await load();
                    } catch (e) {
                      setError(errorMessage(e));
                    }
                  }}
                >
                  Remove
                </button>
              )
            )}
          </div>
        ))}
      </div>
      {matter.is_owner && (
        <>
          <form
            onSubmit={async (e) => {
              e.preventDefault();
              const form = e.currentTarget;
              try {
                await api(`/matters/${matter.id}/members`, 'POST', Object.fromEntries(new FormData(form)));
                form.reset();
                await load();
              } catch (e) {
                setError(errorMessage(e));
              }
            }}
          >
            <label>
              Add an existing account
              <input name="email" type="email" placeholder="colleague@firm.com" required />
            </label>
            <Button className="primary">Add member</Button>
          </form>
          <button
            className="text-danger"
            onClick={async () => {
              if (!confirm('Delete this matter? Documents return to their owners’ private libraries.')) return;
              try {
                await api(`/matters/${matter.id}`, 'DELETE');
                onDelete();
              } catch (e) {
                setError(errorMessage(e));
              }
            }}
          >
            Delete matter
          </button>
        </>
      )}
    </Modal>
  );
}

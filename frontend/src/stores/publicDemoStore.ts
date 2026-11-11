import { create } from 'zustand';
import { api, DemoSessionExpired } from '../api';
import type { DemoAnswer, DemoDocument, DemoScope, DemoSession } from '../api/types';

interface DemoState {
  mode: 'loading' | 'demo' | 'full' | 'error';
  scope: DemoScope;
  session: DemoSession | null;
  documents: DemoDocument[];
  messages: (DemoAnswer & { question: string })[];
  question: string;
  error: string;
  busy: boolean;
  epoch: number;
  initialize(): Promise<void>;
  setQuestion(value: string): void;
  select(scope: DemoScope): Promise<void>;
  refresh(): Promise<void>;
  ask(): Promise<void>;
  upload(file: File): Promise<void>;
  logout(): Promise<void>;
}

let initialized: Promise<void> | undefined;
export const usePublicDemoStore = create<DemoState>((set, get) => {
  const failed = (error: unknown, epoch: number) => {
    if (get().epoch !== epoch) return;
    const message = error instanceof Error ? error.message : 'The request failed. Please try again.';
    if (error instanceof DemoSessionExpired) {
      set({ session: null, scope: 'public', documents: [], messages: [], question: '', busy: false, epoch: epoch + 1, error: message });
    } else set({ error: message, busy: false });
  };
  return {
    mode: 'loading', scope: 'public', session: null, documents: [], messages: [], question: '', error: '', busy: false, epoch: 0,
    initialize: () => initialized ??= (async () => {
      // The server marks the demo HTML. Keep normal/offline startup unchanged.
      if (!document.querySelector('meta[name="casecite-public-demo"][content="true"]')) {
        set({ mode: 'full' });
        return;
      }
      try {
        // Never persist the invitation or chat in browser storage.
        const invite = new URLSearchParams(window.location.hash.slice(1)).get('invite');
        if (invite) window.history.replaceState(null, '', window.location.pathname + window.location.search);
        await api.initCsrf();
        if (invite) {
          try { await api.demoRedeem(invite); }
          catch (error) { failed(error, get().epoch); }
        }
        try {
          const session = await api.demoSession();
          set({ session, scope: 'private' });
        } catch (error) {
          if (!(error instanceof DemoSessionExpired)) throw error;
        }
        set({ mode: 'demo' });
        await get().refresh();
      } catch (error) {
        failed(error, get().epoch);
        set({ mode: 'error' });
      }
    })(),
    setQuestion: (question) => set({ question }),
    select: async (scope) => {
      if (scope === 'private' && !get().session) return;
      set({ scope, documents: [], messages: [], question: '', error: '', busy: false, epoch: get().epoch + 1 });
      await get().refresh();
    },
    refresh: async () => {
      const { scope, epoch } = get();
      try {
        const documents = await api.demoDocuments(scope);
        const session = scope === 'private' ? await api.demoSession() : get().session;
        if (get().epoch === epoch) set({ documents, session });
      } catch (error) { failed(error, epoch); }
    },
    ask: async () => {
      const { question, scope, epoch, busy } = get();
      if (!question.trim() || busy) return;
      set({ busy: true, error: '' });
      try {
        const answer = await api.demoChat(scope, question);
        if (get().epoch === epoch) set({ messages: [...get().messages, { question, ...answer }], question: '', busy: false });
      } catch (error) { failed(error, epoch); }
    },
    upload: async (file) => {
      const { scope, epoch, busy } = get();
      if (scope !== 'private' || busy) return;
      set({ busy: true, error: '' });
      try {
        await api.demoUpload(file);
        if (get().epoch !== epoch) return;
        set({ busy: false });
        await get().refresh();
      } catch (error) { failed(error, epoch); }
    },
    logout: async () => {
      // Clear private content immediately. Ignore late responses from this view.
      const epoch = get().epoch + 1;
      set({ session: null, scope: 'public', documents: [], messages: [], question: '', busy: false, error: '', epoch });
      try { await api.demoLogout(); await get().refresh(); }
      catch (error) {
        failed(new Error(`Could not end the private session on the server. Reload and retry ending the session. ${error instanceof Error ? error.message : ''}`), epoch);
      }
    },
  };
});

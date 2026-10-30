import { create } from 'zustand';
import { apiBase, ApiClient, SessionExpiredError, type Tokens, webOrigin } from './api/client';
import type {
  ChatResponse,
  ContractAnalysisResult,
  ContractIssue,
  ContractKeyTerm,
  DeviceStart,
  Matter,
  StoredSession,
} from './api/types';
import { DeviceFlowError, runDeviceFlow } from './auth/deviceFlow';
import { AI_REVIEW_NOTICE } from './notice';
import {
  insertAnswerWithFootnotes,
  readDocumentText,
  replaceQuoteTracked,
  selectQuote,
} from './office/document';
import { MATTER_KEY, SERVER_URL_KEY, SESSION_KEY, createStore, readJson } from './storage';

/**
 * All pane state and behaviour. Screens render it; nothing here touches React.
 */

export type Screen = 'analyze' | 'research' | 'settings';

export interface SignInState {
  status: 'idle' | 'starting' | 'waiting' | 'error';
  code: DeviceStart | null;
  error: string;
}

export interface AnalysisState {
  status: 'idle' | 'reading' | 'running' | 'done' | 'error';
  result: ContractAnalysisResult | null;
  error: string;
  representing: string;
  /** Issue ref whose select/apply is in flight. */
  busyRef: string | null;
  appliedRefs: string[];
  /** Last document action outcome, shown under the list. */
  note: string;
}

export interface ResearchState {
  question: string;
  status: 'idle' | 'running' | 'done' | 'error';
  result: ChatResponse | null;
  error: string;
  inserting: boolean;
  note: string;
}

export interface AddinState {
  booted: boolean;
  serverUrl: string;
  session: StoredSession | null;
  client: ApiClient | null;
  screen: Screen;
  matters: Matter[];
  mattersError: string;
  matterId: string | null;
  signIn: SignInState;
  analysis: AnalysisState;
  research: ResearchState;

  init: () => Promise<void>;
  setScreen: (screen: Screen) => void;
  setServerUrl: (url: string) => Promise<void>;
  startSignIn: () => Promise<void>;
  cancelSignIn: () => void;
  signOut: () => Promise<void>;
  loadMatters: () => Promise<void>;
  setMatter: (matterId: string | null) => Promise<void>;

  setRepresenting: (value: string) => void;
  analyze: () => Promise<void>;
  selectIssue: (issue: ContractIssue) => Promise<void>;
  applyRewrite: (issue: ContractIssue) => Promise<void>;
  selectKeyTerm: (term: ContractKeyTerm) => Promise<void>;

  setQuestion: (value: string) => void;
  ask: () => Promise<void>;
  insertAnswer: () => Promise<void>;
}

const store = createStore();
let signInAbort: AbortController | null = null;
let storageWrites = Promise.resolve();
function persist(write: () => Promise<void>): Promise<void> {
  storageWrites = storageWrites.then(write, write);
  return storageWrites;
}

const initialSignIn: SignInState = { status: 'idle', code: null, error: '' };
const initialAnalysis: AnalysisState = {
  status: 'idle',
  result: null,
  error: '',
  representing: '',
  busyRef: null,
  appliedRefs: [],
  note: '',
};
const initialResearch: ResearchState = {
  question: '',
  status: 'idle',
  result: null,
  error: '',
  inserting: false,
  note: '',
};

function message(e: unknown, fallback: string): string {
  return e instanceof Error && e.message ? e.message : fallback;
}

/** Open the approval page in the system browser (Office) or a new tab (dev). */
export function openApprovalPage(url: string): void {
  try {
    const office = (globalThis as { Office?: typeof Office }).Office;
    if (office?.context?.ui?.openBrowserWindow) {
      office.context.ui.openBrowserWindow(url);
      return;
    }
  } catch {}
  try {
    window.open(url, '_blank', 'noopener');
  } catch {}
}

/** The approval URL for a code: the server's answer, made absolute if it was a bare path. */
export function approvalUrl(serverUrl: string, code: DeviceStart): string {
  const uri = code.verification_uri_complete || code.verification_uri;
  if (/^https?:\/\//i.test(uri)) return uri;
  return `${webOrigin(serverUrl)}${uri.startsWith('/') ? '' : '/'}${uri}`;
}

export const useAddinStore = create<AddinState>((set, get) => {
  let researchVersion = 0;

  function clearSession(error = ''): void {
    signInAbort?.abort();
    signInAbort = null;
    researchVersion++;
    set({
      session: null, client: get().serverUrl ? makeClient(get().serverUrl, null) : null,
      matters: [], mattersError: '', matterId: null, screen: 'settings',
      signIn: error ? { status: 'error', code: null, error } : initialSignIn,
      analysis: initialAnalysis, research: initialResearch,
    });
    void persist(async () => {
      await store.remove(SESSION_KEY);
      await store.remove(MATTER_KEY);
    });
  }

  function makeClient(serverUrl: string, tokens: Tokens | null): ApiClient {
    const client = new ApiClient({
      serverUrl,
      tokens,
      onTokens: (next) => {
        // A refresh/logout from a former server or session cannot alter the
        // current account, including its persisted refresh token.
        if (get().client !== client) return;
        const { session } = get();
        if (!next) {
          if (session) clearSession('Your CaseCite session has ended. Sign in again.');
          return;
        }
        if (session) {
          const updated = { ...session, accessToken: next.accessToken, refreshToken: next.refreshToken };
          set({ session: updated });
          void persist(() => store.set(SESSION_KEY, JSON.stringify(updated)));
        }
      },
    });
    return client;
  }

  /** A failed call that means "signed out" clears the session; everything else is reported. */
  function handleFailure(e: unknown, fallback: string): string {
    if (e instanceof SessionExpiredError) {
      clearSession(message(e, 'Sign in again.'));
    }
    return message(e, fallback);
  }

  return {
    booted: false,
    serverUrl: '',
    session: null,
    client: null,
    screen: 'analyze',
    matters: [],
    mattersError: '',
    matterId: null,
    signIn: initialSignIn,
    analysis: initialAnalysis,
    research: initialResearch,

    init: async () => {
      if (get().booted) return;
      const remembered = await readJson<StoredSession>(store, SESSION_KEY);
      const embeddedOrigin = typeof window !== 'undefined' && window.location.pathname.startsWith('/word-addin/') ? window.location.origin : '';
      const savedUrl = (await store.get(SERVER_URL_KEY)) || remembered?.serverUrl || embeddedOrigin;
      const session = remembered && typeof remembered.serverUrl === 'string'
        && typeof remembered.accessToken === 'string' && typeof remembered.refreshToken === 'string'
        && remembered.user && apiBase(remembered.serverUrl) === apiBase(savedUrl) ? remembered : null;
      const matterId = session ? (await store.get(MATTER_KEY)) || null : null;
      if (remembered && !session) await persist(() => store.remove(SESSION_KEY));
      const tokens = session ? { accessToken: session.accessToken, refreshToken: session.refreshToken } : null;
      set({
        booted: true,
        serverUrl: savedUrl,
        session,
        matterId,
        client: savedUrl ? makeClient(savedUrl, tokens) : null,
        screen: session ? 'analyze' : 'settings',
      });
      if (session) await get().loadMatters();
    },

    setScreen: (screen) => set({ screen }),

    setServerUrl: async (url) => {
      const serverUrl = url.trim().replace(/\/+$/, '');
      if (serverUrl === get().serverUrl) return;
      clearSession();
      set({ serverUrl, client: serverUrl ? makeClient(serverUrl, null) : null });
      await persist(() => store.set(SERVER_URL_KEY, serverUrl));
    },

    startSignIn: async () => {
      const { client, serverUrl } = get();
      if (!client || !serverUrl) {
        set({ signIn: { ...initialSignIn, status: 'error', error: 'Enter your CaseCite server address first.' } });
        return;
      }
      signInAbort?.abort();
      const abort = new AbortController();
      signInAbort = abort;
      set({ signIn: { status: 'starting', code: null, error: '' } });
      try {
        const { tokens, user } = await runDeviceFlow(client, {
          signal: abort.signal,
          onCode: (code) => {
            if (abort.signal.aborted || get().client !== client) return;
            set({ signIn: { status: 'waiting', code, error: '' } });
            openApprovalPage(approvalUrl(serverUrl, code));
          },
        });
        if (abort.signal.aborted || get().client !== client) return;
        const session: StoredSession = {
          serverUrl,
          accessToken: tokens.accessToken,
          refreshToken: tokens.refreshToken,
          user,
        };
        set({ session, signIn: initialSignIn, screen: 'analyze' });
        await persist(() => store.set(SESSION_KEY, JSON.stringify(session)));
        if (abort.signal.aborted || get().client !== client) return;
        await get().loadMatters();
      } catch (e: unknown) {
        if (signInAbort !== abort || get().client !== client) return;
        if (e instanceof DeviceFlowError && e.reason === 'cancelled') {
          set({ signIn: initialSignIn });
          return;
        }
        set({ signIn: { status: 'error', code: null, error: message(e, 'Sign-in failed.') } });
      } finally {
        if (signInAbort === abort) signInAbort = null;
      }
    },

    cancelSignIn: () => {
      signInAbort?.abort();
      signInAbort = null;
      set({ signIn: initialSignIn });
    },

    signOut: async () => {
      const { client } = get();
      clearSession();
      if (client) await client.logout();
      await storageWrites;
    },

    loadMatters: async () => {
      const { client, session } = get();
      if (!client || !session) return;
      try {
        const matters = await client.listMatters();
        if (get().client !== client) return;
        const { matterId } = get();
        if (matterId && !matters.some(m => m.id === matterId)) await get().setMatter(null);
        if (get().client !== client) return;
        set({
          matters,
          mattersError: '',
        });
      } catch (e: unknown) {
        if (get().client !== client) return;
        const error = handleFailure(e, 'Could not load matters.');
        if (get().client === client) set({ matters: [], mattersError: error });
      }
    },

    setMatter: async (matterId) => {
      if (matterId === get().matterId) return;
      researchVersion++;
      set({ matterId, research: { ...initialResearch, question: get().research.question } });
      await persist(() => matterId ? store.set(MATTER_KEY, matterId) : store.remove(MATTER_KEY));
    },

    setRepresenting: (value) => set({ analysis: { ...get().analysis, representing: value } }),

    analyze: async () => {
      const { client, session, analysis } = get();
      if (!client || !session || analysis.busyRef || analysis.status === 'reading' || analysis.status === 'running') return;
      set({ analysis: { ...analysis, status: 'reading', error: '', note: '' } });
      let text = '';
      try {
        text = await readDocumentText();
      } catch (e: unknown) {
        if (get().client !== client) return;
        set({ analysis: { ...get().analysis, status: 'error', error: message(e, 'Could not read the document.') } });
        return;
      }
      if (get().client !== client) return;
      if (!text.trim()) {
        set({ analysis: { ...get().analysis, status: 'error', error: 'The document is empty.' } });
        return;
      }
      set({ analysis: { ...get().analysis, status: 'running' } });
      try {
        const result = await client.analyzeText(text, analysis.representing || undefined);
        if (get().client !== client) return;
        set({ analysis: { ...get().analysis, status: 'done', result, appliedRefs: [] } });
      } catch (e: unknown) {
        if (get().client !== client) return;
        const error = handleFailure(e, 'Analysis failed.');
        if (get().client === client) set({ analysis: { ...get().analysis, status: 'error', error } });
      }
    },

    selectIssue: async (issue) => {
      const { client } = get();
      if (get().analysis.busyRef) return;
      set({ analysis: { ...get().analysis, busyRef: issue.ref, note: '' } });
      try {
        const hit = await selectQuote(get().analysis.appliedRefs.includes(issue.ref) ? issue.suggested_language : issue.matched_text);
        if (get().client !== client) return;
        set({
          analysis: {
            ...get().analysis,
            busyRef: null,
            note: hit.found ? '' : 'That passage was not found in the document as written.',
          },
        });
      } catch (e: unknown) {
        if (get().client !== client) return;
        set({ analysis: { ...get().analysis, busyRef: null, note: message(e, 'Could not select the passage.') } });
      }
    },

    applyRewrite: async (issue) => {
      const { client } = get();
      if (!issue.suggested_language || get().analysis.busyRef || get().analysis.appliedRefs.includes(issue.ref) || issue.grounding !== 'span') return;
      set({ analysis: { ...get().analysis, busyRef: issue.ref, note: '' } });
      try {
        const hit = await replaceQuoteTracked(issue.matched_text, issue.suggested_language);
        if (get().client !== client) return;
        set({
          analysis: {
            ...get().analysis,
            busyRef: null,
            appliedRefs: hit.found ? [...get().analysis.appliedRefs, issue.ref] : get().analysis.appliedRefs,
            note: hit.found
              ? `${hit.warning || 'Inserted as a tracked change.'} ${AI_REVIEW_NOTICE}`
              : hit.reason === 'ambiguous' || hit.reason === 'too_many_matches'
                ? 'More than one passage could match. Select the complete intended passage in Word, then try again.'
                : hit.reason === 'changed'
                  ? 'The passage changed while it was being located. Review the current document and analyze it again.'
                  : 'The complete quoted passage was not found in the document, so nothing was changed.',
          },
        });
      } catch (e: unknown) {
        if (get().client !== client) return;
        set({ analysis: { ...get().analysis, busyRef: null, note: message(e, 'Could not insert the rewrite.') } });
      }
    },

    selectKeyTerm: async (term) => {
      const { client } = get();
      if (get().analysis.busyRef) return;
      try {
        const hit = await selectQuote(term.quote);
        if (get().client !== client) return;
        set({
          analysis: {
            ...get().analysis,
            note: hit.found ? '' : 'That passage was not found in the document as written.',
          },
        });
      } catch (e: unknown) {
        if (get().client !== client) return;
        set({ analysis: { ...get().analysis, note: message(e, 'Could not select the passage.') } });
      }
    },

    setQuestion: (value) => set({ research: { ...get().research, question: value } }),

    ask: async () => {
      const { client, session, research, matterId } = get();
      if (!client || !session || research.status === 'running' || research.inserting) return;
      const question = research.question.trim();
      if (!question) return;
      const version = ++researchVersion;
      const current = () => get().client === client && researchVersion === version;
      set({ research: { ...research, status: 'running', result: null, error: '', note: '' } });
      try {
        let documentIds: string[] | undefined;
        if (matterId) {
          const docs = await client.listMatterDocuments(matterId);
          if (!current()) return;
          documentIds = docs.map((d) => d.id);
          if (documentIds.length === 0) {
            set({
              research: {
                ...get().research,
                status: 'error',
                error: 'The selected matter has no documents yet. Choose another matter or "All my documents".',
              },
            });
            return;
          }
        }
        const result = await client.research(question, documentIds);
        if (!current()) return;
        set({ research: { ...get().research, status: 'done', result } });
      } catch (e: unknown) {
        if (!current()) return;
        const error = handleFailure(e, 'Research failed.');
        if (current()) set({ research: { ...get().research, status: 'error', error } });
      }
    },

    insertAnswer: async () => {
      const { client, research } = get();
      const version = researchVersion;
      const current = () => get().client === client && researchVersion === version;
      if (!research.result || research.inserting) return;
      set({ research: { ...research, inserting: true, note: '' } });
      try {
        const mode = await insertAnswerWithFootnotes(research.result.content, research.result.citations, AI_REVIEW_NOTICE);
        if (!current()) return;
        set({ research: { ...get().research, inserting: false, note: mode === 'sources'
          ? 'Inserted at the cursor with a Sources list. This Word version does not support inserting footnotes.'
          : mode === 'footnotes' ? 'Inserted at the cursor with the sources as footnotes.' : 'Inserted at the cursor.' } });
      } catch (e: unknown) {
        if (!current()) return;
        set({ research: { ...get().research, inserting: false, note: message(e, 'Could not insert the answer.') } });
      }
    },
  };
});

import { create } from 'zustand';
import { registerReset } from './resetRegistry';
import { api, API_BASE_URL } from '../api';
import { formatErrorDetail } from '../api/client';
import { persistWorkspaceSession, resetWorkspaceSessionKey } from './workspaceSessionsStore';
import logger from '../utils/logger';
import { useUIStore } from './uiStore';
import { createJobPoller } from './storeUtils';
import type {
  JudgeSearchResponse, JudgeIntelProfile, JudgeStats, JudgeMetrics,
  JudgeMessage, DocumentFilter, OpinionListResponse, OpinionSummary,
} from '../types';

export type JudgeOpinionSort = 'citations' | 'date';

const judgeBuildPoller = createJobPoller();
let buildGeneration = 0;
let briefRequest: AbortController | null = null;
let questionRequest: AbortController | null = null;
let opinionRequest = 0;
let fullOpinionRequest = 0;
function stopJudgeChat() {
  briefRequest?.abort();
  questionRequest?.abort();
  briefRequest = questionRequest = null;
}
async function readJudgeAnswer(response: Response, fallback: string): Promise<string> {
  const data = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(formatErrorDetail(data.detail, fallback));
  if (typeof data.content !== 'string' || !data.content.trim()) throw new Error('The model returned an empty answer. Please retry.');
  return data.content;
}
let settleBuild: (() => void) | null = null;
function stopJudgeBuildPolling() {
  buildGeneration += 1;
  opinionRequest += 1;
  fullOpinionRequest += 1;
  stopJudgeChat();
  judgeBuildPoller.stop();
  settleBuild?.();
  settleBuild = null;
}
interface JudgeBuildJob {
  status: string;
  result?: { profile?: JudgeIntelProfile };
  error?: string | null;
  progress?: { message?: string };
}

export interface JudgeIntelState {
  // Search & profile
  judgeIntelSearch: string;
  setJudgeIntelSearch: (val: string) => void;
  judgeIntelResults: JudgeSearchResponse | null;
  judgeIntelProfile: JudgeIntelProfile | null;
  judgeIntelStats: JudgeStats | null;
  judgeIntelMetrics: JudgeMetrics | null;
  judgeIntelMetricsLoading: boolean;
  judgeIntelOpinionsLoading: boolean;
  judgeIntelLoading: boolean;
  judgeIntelBuilding: boolean;
  judgeIntelBuildProgress: string;
  // Brief sub-state
  judgeBrief: string | null;
  judgeBriefLoading: boolean;
  judgeMessages: JudgeMessage[];
  judgeQueryInput: string;
  setJudgeQueryInput: (val: string) => void;
  judgeQueryLoading: boolean;
  judgeQueryIncludeDocs: boolean;
  clearJudgeConversation: () => void;
  setJudgeQueryIncludeDocs: (val: boolean) => void;
  judgeDocFilter: DocumentFilter | null;
  setJudgeDocFilter: (val: DocumentFilter | null) => void;
  showJudgeDocSelector: boolean;
  setShowJudgeDocSelector: (val: boolean) => void;
  // Opinion sub-state
  judgeIntelOpinions: OpinionListResponse | null;
  judgeIntelOpinionQuery: string;
  setJudgeIntelOpinionQuery: (val: string) => void;
  judgeIntelOpinionSort: JudgeOpinionSort;
  selectedOpinion: OpinionSummary | null;
  setSelectedOpinion: (val: OpinionSummary | null) => void;
  // Actions
  searchJudgesIntel: () => Promise<void>;
  buildJudgeIntel: (judgeId: string | number, judgeName: string, forceRefresh?: boolean) => Promise<void>;
  /** Re-pull everything from CourtListener for the judge on screen. */
  rebuildJudgeIntel: () => Promise<void>;
  loadJudgeIntelStats: (judgeId: string | number) => Promise<void>;
  loadJudgeIntelMetrics: (judgeId: string | number) => Promise<void>;
  backToJudgeSearch: () => void;
  generateJudgeBrief: (profile: JudgeIntelProfile) => Promise<void>;
  queryJudgeContext: () => Promise<void>;
  queryJudgeOpinions: (
    judgeId: string | number, query?: string, offset?: number, sort?: JudgeOpinionSort
  ) => Promise<void>;
  loadOpinionFullText: (judgeId: string | number, opinionId: string | number) => Promise<void>;
  /** Rebuild a judge session (profile + Q&A + brief) from History. */
  restoreWorkspaceSession: (payload: Record<string, unknown>) => Promise<boolean>;
}

export const useJudgeIntelStore = create<JudgeIntelState>((set, get) => {
  // Universal History: the judge Q&A + brief snapshot; the profile itself is
  // rebuilt from the local judge cache on restore.
  const persistJudgeSession = () => {
    const { judgeIntelProfile, judgeMessages, judgeBrief, judgeQueryIncludeDocs, judgeDocFilter } = get();
    if (!judgeIntelProfile?.id) return;
    persistWorkspaceSession('judge', 'judge', `Judge: ${judgeIntelProfile.name || judgeIntelProfile.id}`, {
      judgeId: judgeIntelProfile.id,
      judgeName: judgeIntelProfile.name || '',
      judgeMessages,
      judgeBrief,
      judgeQueryIncludeDocs,
      judgeDocFilter,
    });
  };

  const isCurrentJudge = (id: string | number, generation: number) =>
    generation === buildGeneration && String(get().judgeIntelProfile?.id) === String(id);

  return ({
  // Search & profile
  judgeIntelSearch: '',
  setJudgeIntelSearch: (val) => set({ judgeIntelSearch: val }),
  judgeIntelResults: null,
  judgeIntelProfile: null,
  judgeIntelStats: null,
  judgeIntelMetrics: null,
  judgeIntelMetricsLoading: false,
  judgeIntelOpinionsLoading: false,
  judgeIntelLoading: false,
  judgeIntelBuilding: false,
  judgeIntelBuildProgress: '',

  // Brief sub-state
  judgeBrief: null,
  judgeBriefLoading: false,
  judgeMessages: [],
  judgeQueryInput: '',
  setJudgeQueryInput: (val) => set({ judgeQueryInput: val }),
  judgeQueryLoading: false,
  judgeQueryIncludeDocs: false,
  setJudgeQueryIncludeDocs: (val) => set({ judgeQueryIncludeDocs: val }),
  clearJudgeConversation: () => {
    questionRequest?.abort();
    questionRequest = null;
    set({ judgeMessages: [], judgeQueryInput: '', judgeQueryLoading: false });
    persistJudgeSession();
  },
  judgeDocFilter: null,
  setJudgeDocFilter: (val) => set({ judgeDocFilter: val }),
  showJudgeDocSelector: false,
  setShowJudgeDocSelector: (val) => set({ showJudgeDocSelector: val }),

  // Opinion sub-state
  judgeIntelOpinions: null,
  judgeIntelOpinionQuery: '',
  setJudgeIntelOpinionQuery: (val) => set({ judgeIntelOpinionQuery: val }),
  judgeIntelOpinionSort: 'citations',
  selectedOpinion: null,
  setSelectedOpinion: (val) => { fullOpinionRequest += 1; set({ selectedOpinion: val }); },

  // Actions
  loadJudgeIntelStats: async (judgeId) => {
    const generation = buildGeneration;
    try {
      const response = await api.authFetch(`${API_BASE_URL}/judge-intel/profile/${judgeId}/stats`);
      const data = await response.json().catch(() => ({}));
      if (!isCurrentJudge(judgeId, generation)) return;
      if (response.ok) {
        set({ judgeIntelStats: data });
      } else {
        useUIStore.getState().addToast('Could not load judge statistics.', 'error');
      }
    } catch (error: unknown) {
      if (!isCurrentJudge(judgeId, generation)) return;
      logger.error('Stats error:', error);
      useUIStore.getState().addToast('Could not load judge statistics.', 'error');
    }
  },

  loadJudgeIntelMetrics: async (judgeId) => {
    const generation = buildGeneration;
    set({ judgeIntelMetricsLoading: true });
    try {
      const response = await api.authFetch(`${API_BASE_URL}/judge-intel/profile/${judgeId}/metrics`);
      const data = await response.json().catch(() => ({}));
      if (!isCurrentJudge(judgeId, generation)) return;
      if (response.ok) {
        set({ judgeIntelMetrics: data });
      } else {
        set({ judgeIntelMetrics: { metrics: {}, error: formatErrorDetail(data.detail, 'Analytics unavailable') } });
      }
    } catch (error: unknown) {
      if (!isCurrentJudge(judgeId, generation)) return;
      logger.error('Metrics error:', error);
      set({ judgeIntelMetrics: { metrics: {}, error: 'Analytics unavailable' } });
    }
    if (isCurrentJudge(judgeId, generation)) set({ judgeIntelMetricsLoading: false });
  },

  rebuildJudgeIntel: async () => {
    const { judgeIntelProfile } = get();
    if (!judgeIntelProfile?.id) return;
    await get().buildJudgeIntel(judgeIntelProfile.id, judgeIntelProfile.name || '', true);
  },

  searchJudgesIntel: async () => {
    const { judgeIntelSearch } = get();
    if (!judgeIntelSearch.trim()) return;
    get().backToJudgeSearch();
    const generation = buildGeneration;
    set({ judgeIntelLoading: true, judgeIntelResults: null });
    try {
      const response = await api.authFetch(`${API_BASE_URL}/judge-intel/search?q=${encodeURIComponent(judgeIntelSearch)}&limit=500`);
      if (!response.ok) {
        const errorData = await response.json().catch(() => ({}));
        throw new Error(formatErrorDetail(errorData.detail, 'Search failed'));
      }
      const data = await response.json();
      if (generation === buildGeneration) set({ judgeIntelResults: data });
    } catch (error: unknown) {
      if (generation !== buildGeneration) return;
      logger.error('Judge Intel search error:', error);
      set({ judgeIntelResults: { error: error instanceof Error ? error.message : String(error) } });
    }
    if (generation === buildGeneration) set({ judgeIntelLoading: false });
  },

  buildJudgeIntel: async (judgeId, judgeName, forceRefresh = false) => {
    stopJudgeBuildPolling();
    const generation = buildGeneration;
    // A new judge is a new History session.
    resetWorkspaceSessionKey('judge');
    set({
      judgeIntelBuilding: true,
      judgeIntelBuildProgress: forceRefresh
        ? `Re-pulling everything CourtListener has on ${judgeName}...`
        : `Starting analysis of ${judgeName}...`,
      judgeIntelProfile: null,
      judgeIntelLoading: false,
      judgeIntelStats: null,
      judgeIntelMetrics: null,
      judgeIntelMetricsLoading: false,
      judgeIntelOpinionsLoading: false,
      judgeBriefLoading: false,
      judgeQueryLoading: false,
      judgeIntelOpinions: null,
      judgeIntelOpinionQuery: '',
      judgeIntelOpinionSort: 'citations',
      selectedOpinion: null,
      judgeBrief: null,
      judgeMessages: [],
      judgeQueryInput: '',
    });

    try {
      const response = await api.authFetch(
        `${API_BASE_URL}/judge-intel/build/${judgeId}?async=true${forceRefresh ? '&force_refresh=true' : ''}`,
        { method: 'POST' },
      );
      if (!response.ok) {
        const errorData = await response.json().catch(() => ({}));
        throw new Error(formatErrorDetail(errorData.detail, 'Build failed'));
      }
      const data = await response.json();
      if (generation !== buildGeneration) return;
      let profile: JudgeIntelProfile | undefined = data.profile;
      if (response.status === 202) {
        profile = await new Promise<JudgeIntelProfile | undefined>((resolve, reject) => {
          settleBuild = () => resolve(undefined);
          judgeBuildPoller.start<JudgeBuildJob>({
            fetchJob: () => api.request(`/jobs/${encodeURIComponent(data.job_id)}`),
            onPending: (job) => set({ judgeIntelBuildProgress: job.progress?.message || `Building ${judgeName}'s profile…` }),
            onCompleted: (job) => { settleBuild = null; resolve(job.result?.profile); },
            onFailed: (message) => { settleBuild = null; reject(new Error(message)); },
            maxAttempts: 1200,
            intervalMs: 3000,
            failedMessage: 'Judge profile build failed.',
            timeoutMessage: 'The profile is still building. Open this judge again to reconnect to the build.',
          });
        });
      }
      if (generation !== buildGeneration) return;
      if (!profile) throw new Error('The build returned no judge profile. Please retry.');
      set({ judgeIntelProfile: profile, judgeIntelBuildProgress: '', judgeBrief: null });
      // The page shows opinions and statistics inline (no tabs) — load both
      // right away. Opinions default to most-cited order.
      get().queryJudgeOpinions(judgeId);
      get().loadJudgeIntelStats(judgeId);
      get().loadJudgeIntelMetrics(judgeId);
    } catch (error: unknown) {
      if (generation !== buildGeneration) return;
      logger.error('Build intel error:', error);
      set({ judgeIntelProfile: { error: error instanceof Error ? error.message : String(error) } as JudgeIntelProfile });
    }
    if (generation === buildGeneration) set({ judgeIntelBuilding: false });
  },

  backToJudgeSearch: () => {
    stopJudgeBuildPolling();
    set({
      judgeIntelBuilding: false,
      judgeIntelBuildProgress: '',
      judgeIntelProfile: null,
      judgeIntelLoading: false,
      judgeIntelStats: null,
      judgeIntelMetrics: null,
      judgeIntelMetricsLoading: false,
      judgeIntelOpinionsLoading: false,
      judgeBriefLoading: false,
      judgeQueryLoading: false,
      judgeIntelOpinions: null,
      judgeIntelOpinionQuery: '',
      judgeIntelOpinionSort: 'citations',
      selectedOpinion: null,
      judgeBrief: null,
      judgeMessages: [],
      judgeQueryInput: '',
    });
  },

  generateJudgeBrief: async (profile) => {
    const generation = buildGeneration;
    if (!profile?.id || profile.error || get().judgeBriefLoading || !isCurrentJudge(profile.id, generation)) return;
    const controller = new AbortController();
    briefRequest = controller;
    const current = () => briefRequest === controller && isCurrentJudge(profile.id!, generation);
    set({ judgeBriefLoading: true });
    try {
      const response = await api.authFetch(`${API_BASE_URL}/chat`, {
        method: 'POST', timeout: 180000, signal: controller.signal,
        body: JSON.stringify({
          query: `Prepare a judicial intelligence brief for ${profile.name || 'this judge'}.`,
          judge_id: String(profile.id), judge_task: 'brief', mode: 'research',
          include_documents: false, include_case_law: false,
        }),
      });
      const content = await readJudgeAnswer(response, 'Failed to generate brief');
      if (!current()) return;
      set({ judgeBrief: content });
      persistJudgeSession();
    } catch (error: unknown) {
      if (!current() || controller.signal.aborted) return;
      logger.error('Judge brief error:', error);
      useUIStore.getState().addToast(error instanceof Error ? error.message : 'Failed to generate brief', 'error');
    } finally {
      if (current()) { briefRequest = null; set({ judgeBriefLoading: false }); }
    }
  },

  queryJudgeContext: async () => {
    const { judgeQueryInput, judgeIntelProfile, judgeQueryIncludeDocs, judgeDocFilter, judgeQueryLoading } = get();
    if (!judgeQueryInput.trim() || !judgeIntelProfile?.id || judgeIntelProfile.error || judgeQueryLoading) return;
    const generation = buildGeneration;
    const controller = new AbortController();
    questionRequest = controller;
    const current = () => questionRequest === controller && isCurrentJudge(judgeIntelProfile.id!, generation);
    const userQuery = judgeQueryInput.trim();
    const userMsg: JudgeMessage = { id: Date.now(), type: 'user', content: userQuery };
    set((state) => ({ judgeQueryInput: '', judgeQueryLoading: true, judgeMessages: [...state.judgeMessages, userMsg] }));
    try {
      const response = await api.authFetch(`${API_BASE_URL}/chat`, {
        method: 'POST', timeout: 180000, signal: controller.signal,
        body: JSON.stringify({
          query: userQuery, judge_id: String(judgeIntelProfile.id), judge_task: 'question', mode: 'research',
          include_documents: judgeQueryIncludeDocs, include_case_law: false,
          document_filter: judgeQueryIncludeDocs ? judgeDocFilter : null,
        }),
      });
      const content = await readJudgeAnswer(response, 'Query failed');
      if (!current()) return;
      const assistantMsg: JudgeMessage = { id: Date.now() + 1, type: 'assistant', content };
      set((state) => ({ judgeMessages: [...state.judgeMessages, assistantMsg] }));
      persistJudgeSession();
    } catch (error: unknown) {
      if (!current() || controller.signal.aborted) return;
      const errorMsg: JudgeMessage = { id: Date.now() + 1, type: 'assistant', content: `Error: ${error instanceof Error ? error.message : String(error)}` };
      set((state) => ({ judgeMessages: [...state.judgeMessages, errorMsg] }));
    } finally {
      if (current()) { questionRequest = null; set({ judgeQueryLoading: false }); }
    }
  },

  queryJudgeOpinions: async (judgeId, query = '', offset = 0, sort) => {
    const generation = buildGeneration;
    const request = ++opinionRequest;
    const current = () => request === opinionRequest && isCurrentJudge(judgeId, generation);
    const order = sort ?? get().judgeIntelOpinionSort;
    set({ judgeIntelOpinionSort: order, judgeIntelOpinionsLoading: true });
    try {
      const apiOrder = order === 'citations' ? 'citations' : 'date';
      let url = `${API_BASE_URL}/judge-intel/profile/${judgeId}/opinions?limit=50&offset=${offset}&order=${apiOrder}`;
      if (query) url += `&q=${encodeURIComponent(query)}`;

      const response = await api.authFetch(url);
      if (!response.ok) {
        const errorData = await response.json().catch(() => ({}));
        throw new Error(formatErrorDetail(errorData.detail, 'Could not load opinions'));
      }
      const data = await response.json();
      if (!current()) return;

      const { judgeIntelOpinions } = get();
      if (offset > 0 && judgeIntelOpinions?.opinions) {
        set({
          judgeIntelOpinions: {
            ...data,
            opinions: [...judgeIntelOpinions.opinions, ...data.opinions],
          },
        });
      } else {
        set({ judgeIntelOpinions: data });
      }
    } catch (error: unknown) {
      if (!current()) return;
      logger.error('Opinions query error:', error);
      useUIStore.getState().addToast(error instanceof Error ? error.message : 'Could not load opinions', 'error');
    }
    if (current()) set({ judgeIntelOpinionsLoading: false });
  },

  loadOpinionFullText: async (judgeId, opinionId) => {
    const generation = buildGeneration;
    const request = ++fullOpinionRequest;
    const current = () => request === fullOpinionRequest && isCurrentJudge(judgeId, generation);
    try {
      const response = await api.authFetch(`${API_BASE_URL}/judge-intel/profile/${judgeId}/opinion/${opinionId}`);
      const data = await response.json().catch(() => ({}));
      if (!current()) return;
      if (response.ok) {
        set({ selectedOpinion: data });
      } else {
        useUIStore.getState().addToast(formatErrorDetail(data.detail, 'Could not load that opinion'), 'error');
      }
    } catch (error: unknown) {
      if (!current()) return;
      logger.error('Opinion load error:', error);
      useUIStore.getState().addToast('Could not load that opinion', 'error');
    }
  },

  restoreWorkspaceSession: async (payload) => {
    const p = payload as {
      judgeId?: string | number;
      judgeName?: string;
      judgeMessages?: JudgeMessage[];
      judgeBrief?: string | null;
      judgeQueryIncludeDocs?: boolean;
      judgeDocFilter?: DocumentFilter | null;
    };
    if (!p.judgeId) return false;
    // The profile rebuilds from the local judge cache (instant when cached);
    // then the saved conversation and brief drop back in on top.
    const build = get().buildJudgeIntel(p.judgeId, p.judgeName || '');
    const generation = buildGeneration;
    await build;
    if (!isCurrentJudge(p.judgeId, generation)) return false;
    set({
      judgeMessages: Array.isArray(p.judgeMessages) ? p.judgeMessages : [],
      judgeBrief: p.judgeBrief ?? null,
      judgeQueryIncludeDocs: p.judgeQueryIncludeDocs === true,
      judgeDocFilter: p.judgeDocFilter ?? null,
    });
    return true;
  },
});
});

registerReset(() => {
  stopJudgeBuildPolling();
  useJudgeIntelStore.setState(useJudgeIntelStore.getInitialState(), true);
});

import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useAddinStore } from './store';
import { insertAnswerWithFootnotes, readDocumentText, replaceQuoteTracked, selectQuote, type SelectionResult } from './office/document';
import { ApiClient, SessionExpiredError } from './api/client';
import type { ChatResponse, ContractAnalysisResult, ContractIssue, Matter, StoredSession } from './api/types';
import { SESSION_KEY, SERVER_URL_KEY } from './storage';

vi.mock('./office/document', () => ({
  replaceQuoteTracked: vi.fn(), selectQuote: vi.fn(), readDocumentText: vi.fn(), insertAnswerWithFootnotes: vi.fn(),
}));

const initial = useAddinStore.getState();
const issue: ContractIssue = { ref: 'r1', title: 'Liability', status: 'risk', severity: 'major', why: 'Review the cap.', matched_text: 'Liability is unlimited.', suggested_language: 'Liability is capped.', grounding: 'span' };
const session: StoredSession = { serverUrl: 'https://casecite.example', accessToken: 'fixture-access', refreshToken: 'fixture-refresh', user: { id: 'u1', email: 'reader@example.test', name: 'Reader', roles: ['attorney'] } };
const answer = { content: 'Notice is 30 days.', citations: [] } as unknown as ChatResponse;
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
function signedIn() {
  const client = new ApiClient({ serverUrl: session.serverUrl, tokens: session });
  vi.spyOn(client, 'logout').mockResolvedValue();
  useAddinStore.setState({ client, session, serverUrl: session.serverUrl });
  return client;
}

describe('Word rewrite feedback and admission', () => {
  beforeEach(() => {
    vi.resetAllMocks();
    useAddinStore.setState(initial, true);
  });

  it('explains how to resolve ambiguous source text without claiming an insertion', async () => {
    vi.mocked(replaceQuoteTracked).mockResolvedValue({ found: false, reason: 'ambiguous' });
    await useAddinStore.getState().applyRewrite(issue);
    const analysis = useAddinStore.getState().analysis;
    expect(analysis.note).toContain('Select the complete intended passage');
    expect(analysis.note).not.toContain('Inserted');
    expect(analysis.busyRef).toBeNull();
  });

  it('distinguishes text changed during lookup from an absent quote', async () => {
    vi.mocked(replaceQuoteTracked).mockResolvedValue({ found: false, reason: 'changed' });
    await useAddinStore.getState().applyRewrite(issue);
    expect(useAddinStore.getState().analysis.note).toContain('changed while it was being located');
    vi.mocked(replaceQuoteTracked).mockResolvedValue({ found: false });
    await useAddinStore.getState().applyRewrite(issue);
    expect(useAddinStore.getState().analysis.note).toContain('complete quoted passage was not found');
  });

  it('does not enqueue another rewrite or selection while one is pending', async () => {
    let finish!: (value: SelectionResult) => void;
    vi.mocked(replaceQuoteTracked).mockImplementation(() => new Promise(resolve => { finish = resolve; }));
    const first = useAddinStore.getState().applyRewrite(issue);
    expect(useAddinStore.getState().analysis.busyRef).toBe('r1');
    await useAddinStore.getState().applyRewrite({ ...issue, ref: 'r2' });
    await useAddinStore.getState().selectIssue({ ...issue, ref: 'r2' });
    expect(replaceQuoteTracked).toHaveBeenCalledTimes(1);
    expect(selectQuote).not.toHaveBeenCalled();
    finish({ found: true });
    await first;
    expect(useAddinStore.getState().analysis.busyRef).toBeNull();
  });

  it('does not apply an unverified or absent source even if called directly', async () => {
    await useAddinStore.getState().applyRewrite({ ...issue, grounding: 'unverified' });
    await useAddinStore.getState().applyRewrite({ ...issue, grounding: 'absence' });
    expect(replaceQuoteTracked).not.toHaveBeenCalled();
  });

  it('retains the distinction between an applied edit and a tracking-mode warning', async () => {
    vi.mocked(replaceQuoteTracked).mockResolvedValue({ found: true, warning: 'The edit was inserted, but the previous tracking mode could not be restored.' });
    await useAddinStore.getState().applyRewrite(issue);
    expect(useAddinStore.getState().analysis.note).toContain('edit was inserted');
    expect(useAddinStore.getState().analysis.note).toContain('could not be restored');
  });

  it('does not apply the same finding to a different duplicate after one successful rewrite', async () => {
    vi.mocked(replaceQuoteTracked).mockResolvedValue({ found: true });
    vi.mocked(selectQuote).mockResolvedValue({ found: true });
    await useAddinStore.getState().applyRewrite(issue);
    await useAddinStore.getState().applyRewrite(issue);
    expect(replaceQuoteTracked).toHaveBeenCalledOnce();
    expect(useAddinStore.getState().analysis.appliedRefs).toEqual(['r1']);
    await useAddinStore.getState().selectIssue(issue);
    expect(selectQuote).toHaveBeenCalledWith(issue.suggested_language);
  });
});

describe('Word pane session and research boundaries', () => {
  beforeEach(() => {
    vi.resetAllMocks();
    localStorage.clear();
    useAddinStore.setState(initial, true);
  });

  it('never restores a token saved for a different server', async () => {
    localStorage.setItem(SESSION_KEY, JSON.stringify(session));
    localStorage.setItem(SERVER_URL_KEY, 'https://new.example');
    await useAddinStore.getState().init();
    expect(useAddinStore.getState().session).toBeNull();
    expect(useAddinStore.getState().client?.signedIn).toBe(false);
    expect(useAddinStore.getState().screen).toBe('settings');
    expect(localStorage.getItem(SESSION_KEY)).toBeNull();
  });

  it('clears research when the selected matter is no longer available', async () => {
    const client = signedIn();
    vi.spyOn(client, 'listMatters').mockResolvedValue([]);
    useAddinStore.setState({ matterId: 'revoked', research: { ...initial.research, result: answer } });
    await useAddinStore.getState().loadMatters();
    expect(useAddinStore.getState().matterId).toBeNull();
    expect(useAddinStore.getState().research.result).toBeNull();
  });

  it('clears immediately on sign-out and ignores a late analysis response', async () => {
    const client = signedIn();
    const result = deferred<ContractAnalysisResult>();
    const logout = deferred<void>();
    vi.mocked(client.logout).mockReturnValue(logout.promise);
    vi.mocked(readDocumentText).mockResolvedValue('Original contract.');
    const analyze = vi.spyOn(client, 'analyzeText').mockReturnValue(result.promise);
    const pending = useAddinStore.getState().analyze();
    await vi.waitFor(() => expect(analyze).toHaveBeenCalledOnce());
    const signingOut = useAddinStore.getState().signOut();
    expect(useAddinStore.getState().session).toBeNull();
    expect(useAddinStore.getState().analysis).toEqual(initial.analysis);
    result.resolve({ issues: [issue] } as unknown as ContractAnalysisResult);
    await pending;
    logout.resolve();
    await signingOut;
    expect(useAddinStore.getState().analysis).toEqual(initial.analysis);
  });

  it('does not send document text if the server changes while Word is reading', async () => {
    const client = signedIn();
    const text = deferred<string>();
    vi.mocked(readDocumentText).mockReturnValue(text.promise);
    const analyze = vi.spyOn(client, 'analyzeText');
    const pending = useAddinStore.getState().analyze();
    await useAddinStore.getState().setServerUrl('https://new.example');
    text.resolve('Private contract from the old session.');
    await pending;
    expect(analyze).not.toHaveBeenCalled();
    expect(useAddinStore.getState().analysis).toEqual(initial.analysis);
  });

  it.each(['resolve', 'reject'] as const)('ignores an old matter listing that will %s after a server switch', async (outcome) => {
    const client = signedIn();
    const listing = deferred<Matter[]>();
    vi.spyOn(client, 'listMatters').mockReturnValue(listing.promise);
    const pending = useAddinStore.getState().loadMatters();
    await useAddinStore.getState().setServerUrl('https://new.example');
    const nextSession = { ...session, serverUrl: 'https://new.example', user: { ...session.user, id: 'u2' } };
    useAddinStore.setState({ session: nextSession });
    if (outcome === 'resolve') listing.resolve([{ id: 'old', name: 'Old matter' } as Matter]);
    else listing.reject(new SessionExpiredError());
    await pending;
    expect(useAddinStore.getState().session).toEqual(nextSession);
    expect(useAddinStore.getState().matters).toEqual([]);
    expect(useAddinStore.getState().mattersError).toBe('');
  });

  it('preserves tokens when saving an unchanged address and ignores an old client token callback', async () => {
    await useAddinStore.getState().setServerUrl(session.serverUrl);
    const client = useAddinStore.getState().client!;
    useAddinStore.setState({ session });
    client.setTokens(session);
    await useAddinStore.getState().setServerUrl(`${session.serverUrl}/`);
    expect(useAddinStore.getState().client).toBe(client);
    expect(client.signedIn).toBe(true);
    await useAddinStore.getState().setServerUrl('https://new.example');
    const next = { ...session, serverUrl: 'https://new.example', accessToken: 'new-fixture' };
    useAddinStore.setState({ session: next });
    client.setTokens({ accessToken: 'old-late-refresh', refreshToken: 'old-late-refresh' });
    client.setTokens(null);
    expect(useAddinStore.getState().session).toEqual(next);
  });

  it('does not publish a delayed answer after switching matters away and back', async () => {
    const client = signedIn();
    const result = deferred<ChatResponse>();
    vi.spyOn(client, 'listMatterDocuments').mockResolvedValue([{ id: 'doc-a' }]);
    const research = vi.spyOn(client, 'research').mockReturnValue(result.promise);
    await useAddinStore.getState().setMatter('a');
    useAddinStore.getState().setQuestion('What notice is required?');
    const pending = useAddinStore.getState().ask();
    await vi.waitFor(() => expect(research).toHaveBeenCalledWith('What notice is required?', ['doc-a']));
    await useAddinStore.getState().setMatter('b');
    await useAddinStore.getState().setMatter('a');
    result.resolve(answer);
    await pending;
    expect(useAddinStore.getState().research.result).toBeNull();
    expect(useAddinStore.getState().research.status).toBe('idle');
  });

  it('clears old answers for a new question and prevents duplicate research submission', async () => {
    const client = signedIn();
    const result = deferred<ChatResponse>();
    const research = vi.spyOn(client, 'research').mockReturnValue(result.promise);
    useAddinStore.setState({ research: { ...initial.research, question: 'New question', result: answer } });
    const pending = useAddinStore.getState().ask();
    await useAddinStore.getState().ask();
    expect(research).toHaveBeenCalledOnce();
    expect(useAddinStore.getState().research.result).toBeNull();
    result.resolve(answer);
    await pending;
    expect(useAddinStore.getState().research.result).toEqual(answer);
  });

  it('reports the Sources-list fallback without claiming footnotes were inserted', async () => {
    signedIn();
    useAddinStore.setState({ research: { ...initial.research, result: answer } });
    vi.mocked(insertAnswerWithFootnotes).mockResolvedValue('sources');
    await useAddinStore.getState().insertAnswer();
    expect(useAddinStore.getState().research.note).toContain('with a Sources list');
    expect(useAddinStore.getState().research.note).toContain('does not support inserting footnotes');
  });
});

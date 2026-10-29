import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react';
import {
  BookOpen,
  FileText,
  FolderOpen,
  History,
  Settings as SettingsIcon,
  LogOut,
  Plus,
  Upload,
  Search,
  ArrowUpRight,
  X,
  Loader2,
  Download,
  Trash2,
  ShieldCheck,
  Scale,
  Layers,
  ArrowRight,
  Users,
} from 'lucide-react';
import { api, url, errorMessage, type User, type Matter, type Document, type Result, type Job } from './api';
import { type View, labels, descriptions, prompts } from './constants';
import { Button, Modal, formatSize, Empty } from './ui';
import { ResultView } from './ResultView';
import { DocumentPreview } from './DocumentPreview';
import { MemberModal } from './MemberModal';
import { SettingsPanel } from './SettingsPanel';

export function Workspace({ user, logout }: { user: User; logout: () => void }) {
  const [view, setView] = useState<View>('research');
  const [matter, setMatter] = useState('');
  const [matters, setMatters] = useState<Matter[]>([]);
  const [docs, setDocs] = useState<Document[]>([]);
  const [selected, setSelected] = useState<string[]>([]);
  const [history, setHistory] = useState<Result[]>([]);
  const [result, setResult] = useState<Result | null>(null);
  const [preview, setPreview] = useState<Document | null>(null);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [uploading, setUploading] = useState(false);
  const [newMatter, setNewMatter] = useState(false);
  const [members, setMembers] = useState(false);
  const [job, setJob] = useState<Job | null>(null);
  const [prompt, setPrompt] = useState('');
  const [cases, setCases] = useState(false);
  const [searchOnly, setSearchOnly] = useState(false);
  const [fields, setFields] = useState('Parties\nEffective date\nTermination notice\nLiability cap\nGoverning law');
  const [busy, setBusy] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);
  const report = (e: unknown) => setError(errorMessage(e));
  const refresh = useCallback(async () => {
    const [d, m, h] = await Promise.all([
      api<{ documents: Document[] }>('/documents'),
      api<{ matters: Matter[] }>('/matters'),
      api<{ results: Result[] }>('/results'),
    ]);
    setDocs(d.documents);
    setMatters(m.matters);
    setHistory(h.results);
    setSelected((s) => s.filter((id) => d.documents.some((d) => d.id === id && d.status === 'indexed')));
  }, []);
  useEffect(() => {
    refresh().catch(report);
    api<{ jobs: Job[] }>('/jobs')
      .then((r) => {
        if (r.jobs.length) setJob(r.jobs[0]);
      })
      .catch(report);
  }, [refresh]);
  useEffect(() => {
    if (!docs.some((d) => ['processing', 'uploading', 'upload_uncertain'].includes(d.status))) return;
    const t = setInterval(() => refresh().catch(report), 4000);
    return () => clearInterval(t);
  }, [docs, refresh]);
  useEffect(() => {
    if (!job || !['pending', 'running'].includes(job.status)) return;
    let stopped = false;
    let timer: ReturnType<typeof setTimeout>;
    const poll = async () => {
      try {
        const j = await api<Job>(`/jobs/${job.job_id}`);
        if (stopped) return;
        if (j.status === 'completed') {
          const r = await api<Result>(`/results/${j.job_id}`);
          if (stopped) return;
          setResult(r);
          setJob(null);
          await refresh();
        } else if (j.status === 'failed' || j.status === 'cancelled') {
          setError(j.error || 'Job stopped');
          setJob(null);
        } else {
          setJob(j);
          timer = setTimeout(poll, 1500);
        }
      } catch (e) {
        if (!stopped) {
          report(e);
          setJob(null);
        }
      }
    };
    timer = setTimeout(poll, 800);
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, [job?.job_id, job?.status, refresh]);
  const scoped = docs.filter((d) => !matter || d.matter_id === matter);
  const activeMatter = matters.find((m) => m.id === matter);
  function navigate(v: View) {
    setView(v);
    setResult(null);
    setError('');
    setNotice('');
    setPrompt('');
  }
  async function upload(files: FileList | null) {
    if (!files?.length) return;
    setUploading(true);
    setError('');
    let count = 0;
    try {
      for (const file of Array.from(files)) {
        const f = new FormData();
        f.append('file', file);
        if (matter) f.append('matter_id', matter);
        await api('/documents', 'POST', f);
        count++;
      }
      setNotice(`${count} document${count === 1 ? '' : 's'} uploaded. Indexing continues in the background.`);
      await refresh();
    } catch (e) {
      report(e);
      await refresh().catch(report);
    } finally {
      setUploading(false);
      if (fileInput.current) fileInput.current.value = '';
    }
  }
  async function run() {
    setError('');
    setBusy(true);
    try {
      const j = await api<Job>('/jobs', 'POST', {
        kind: view,
        prompt: prompt || prompts[view],
        document_ids: selected.filter((id) => scoped.some((d) => d.id === id)),
        matter_id: matter || null,
        include_case_law: cases,
        search_only: searchOnly,
        fields: fields
          .split('\n')
          .map((f) => f.trim())
          .filter(Boolean),
        parent_id: view === 'research' && result?.kind === 'research' ? result.id : undefined,
      });
      setJob(j);
    } catch (e) {
      report(e);
    } finally {
      setBusy(false);
    }
  }
  async function removeDocument(d: Document) {
    if (
      !window.confirm(
        `Delete ${d.filename} and its GoodMem content? Saved results that depend on it will become unavailable.`,
      )
    )
      return;
    try {
      await api(`/documents/${d.id}`, 'DELETE');
      await refresh();
    } catch (e) {
      report(e);
    }
  }
  const nav = (v: View, icon: ReactNode) => (
    <button
      key={v}
      aria-label={labels[v]}
      title={labels[v]}
      className={view === v ? 'nav active' : 'nav'}
      onClick={() => navigate(v)}
    >
      {icon}
      <span>{labels[v]}</span>
    </button>
  );
  return (
    <div className="workspace">
      <aside className="sidebar">
        <a className="brand" href="/">
          <Scale /> CaseCite<span>GOODMEM</span>
        </a>
        <div className="workspace-label">YOUR WORKSPACE</div>
        {nav('research', <Search size={18} />)}
        {nav('documents', <FolderOpen size={18} />)}
        <div className="workspace-label">DOCUMENT TOOLS</div>
        {nav('analyze', <ShieldCheck size={18} />)}
        {nav('draft', <FileText size={18} />)}
        {nav('compare', <Layers size={18} />)}
        {nav('review', <BookOpen size={18} />)}
        <div className="nav-bottom">
          {nav('history', <History size={18} />)}
          {nav('settings', <SettingsIcon size={18} />)}
          <div className="profile">
            <div className="avatar">{user.name.charAt(0).toUpperCase()}</div>
            <div>
              <strong>{user.name}</strong>
              <small>{user.roles.includes('admin') ? 'Administrator' : 'Workspace member'}</small>
            </div>
            <button
              className="icon"
              aria-label="Sign out"
              onClick={async () => {
                try {
                  await api('/auth/logout', 'POST');
                  logout();
                } catch (e) {
                  report(e);
                }
              }}
            >
              <LogOut size={16} />
            </button>
          </div>
        </div>
      </aside>
      <main>
        <header className="topbar">
          <div className="breadcrumb">
            Workspace <span>/</span> {labels[view]}
          </div>
          <div className="matter-control">
            <FolderOpen size={16} />
            <select
              aria-label="Matter scope"
              value={matter}
              onChange={(e) => {
                setMatter(e.target.value);
                setSelected([]);
                setResult(null);
              }}
            >
              <option value="">All accessible documents</option>
              {matters.map((m) => (
                <option key={m.id} value={m.id}>
                  {m.name}
                </option>
              ))}
            </select>
            {activeMatter && (
              <button
                className="icon"
                title="Matter members"
                aria-label="Matter members"
                onClick={() => setMembers(true)}
              >
                <Users size={16} />
              </button>
            )}
            <button className="icon" aria-label="New matter" title="New matter" onClick={() => setNewMatter(true)}>
              <Plus size={17} />
            </button>
          </div>
        </header>
        <div className="content">
          <div className="page-title">
            <div>
              <p className="eyebrow">{activeMatter ? activeMatter.name.toUpperCase() : 'CASE WORKSPACE'}</p>
              <h1>{labels[view]}</h1>
              <p>
                {descriptions[view] ||
                  (view === 'documents'
                    ? 'Your original files, searchable passages, and page images in one place.'
                    : view === 'history'
                      ? 'Return to your research and document work.'
                      : 'Manage your models, account, and workspace.')}
              </p>
            </div>
            {view === 'documents' && (
              <Button className="primary" busy={uploading} onClick={() => fileInput.current?.click()}>
                <Upload size={16} />
                Upload documents
              </Button>
            )}
          </div>
          {error && (
            <div className="banner error" role="alert">
              <span>{error}</span>
              <button className="icon" aria-label="Dismiss error" onClick={() => setError('')}>
                <X size={16} />
              </button>
            </div>
          )}
          {notice && (
            <div className="banner notice" role="status">
              {notice}
              <button className="icon" aria-label="Dismiss notice" onClick={() => setNotice('')}>
                <X size={16} />
              </button>
            </div>
          )}
          <input ref={fileInput} type="file" multiple hidden onChange={(e) => upload(e.target.files)} />
          {view === 'documents' ? (
            <>
              <div className="library-stats">
                <span>
                  <strong>{scoped.length}</strong> documents
                </span>
                <span>
                  <strong>{scoped.filter((d) => d.status === 'indexed').length}</strong> ready for research
                </span>
                <span>Original files & page previews</span>
              </div>
              {scoped.length ? (
                <div className="card">
                  <table className="library">
                    <thead>
                      <tr>
                        <th>Document</th>
                        <th>Status</th>
                        <th>Matter</th>
                        <th>Actions</th>
                      </tr>
                    </thead>
                    <tbody>
                      {scoped.map((d) => (
                        <tr key={d.id}>
                          <td>
                            <button className="document-name" onClick={() => setPreview(d)}>
                              <FileText size={20} />
                              <span>
                                {d.filename}
                                <small>{formatSize(d.size)}</small>
                              </span>
                            </button>
                          </td>
                          <td>
                            <span
                              className={`badge ${d.status === 'indexed' ? 'good' : d.status === 'error' ? 'bad' : ''}`}
                            >
                              {d.status}
                            </span>
                            {d.error && <small className="error">{d.error}</small>}
                          </td>
                          <td>
                            {d.user_id === user.id ? (
                              <select
                                aria-label={`Matter for ${d.filename}`}
                                value={d.matter_id || ''}
                                onChange={async (e) => {
                                  try {
                                    await api(`/documents/${d.id}`, 'PATCH', { matter_id: e.target.value || null });
                                    await refresh();
                                  } catch (e) {
                                    report(e);
                                  }
                                }}
                              >
                                <option value="">Private library</option>
                                {matters.map((m) => (
                                  <option key={m.id} value={m.id}>
                                    {m.name}
                                  </option>
                                ))}
                              </select>
                            ) : (
                              <span>{matters.find((m) => m.id === d.matter_id)?.name}</span>
                            )}
                          </td>
                          <td>
                            <div className="row-actions">
                              <a
                                className="icon"
                                href={url(`/documents/${d.id}/file`)}
                                aria-label={`Download ${d.filename}`}
                              >
                                <Download size={16} />
                              </a>
                              {d.user_id === user.id && (
                                <button
                                  className="icon danger"
                                  aria-label={`Delete ${d.filename}`}
                                  onClick={() => removeDocument(d)}
                                >
                                  <Trash2 size={16} />
                                </button>
                              )}
                            </div>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              ) : (
                <Empty
                  icon={<FolderOpen size={36} />}
                  title="Start with your evidence"
                  text="Upload a PDF, Word document, or text file to make it available for research and review."
                  action={
                    <Button className="primary" busy={uploading} onClick={() => fileInput.current?.click()}>
                      <Upload size={16} />
                      Upload documents
                    </Button>
                  }
                />
              )}
            </>
          ) : view === 'settings' ? (
            <SettingsPanel user={user} report={report} />
          ) : view === 'history' ? (
            result ? (
              <ResultView
                result={result}
                setResult={setResult}
                preview={(id) => setPreview(docs.find((d) => d.id === id) || null)}
                report={report}
              />
            ) : (
              <div className="history-list">
                {history.length ? (
                  history.map((h) => (
                    <button
                      className="history-card"
                      key={h.id}
                      onClick={async () => {
                        try {
                          setResult(await api<Result>(`/results/${h.id}`));
                        } catch (e) {
                          report(e);
                        }
                      }}
                    >
                      <div className="history-icon">
                        <FileText />
                      </div>
                      <div>
                        <span className="eyebrow">{labels[h.kind as View] || h.kind}</span>
                        <h3>{h.title}</h3>
                        <small>{new Date(h.created_at || '').toLocaleString()}</small>
                      </div>
                      <ArrowUpRight size={20} />
                    </button>
                  ))
                ) : (
                  <Empty
                    icon={<History size={36} />}
                    title="Your work will appear here"
                    text="Completed research, reviews, drafts, and comparisons are saved automatically."
                  />
                )}
              </div>
            )
          ) : (
            <div className="work-grid">
              <div className="work-main">
                <section className="composer card">
                  <div className="composer-heading">
                    <span className="step-number">1</span>
                    <h3>{view === 'research' ? 'Ask a question' : 'Give your instructions'}</h3>
                  </div>
                  <textarea
                    aria-label="Instructions"
                    value={prompt}
                    onChange={(e) => setPrompt(e.target.value)}
                    placeholder={prompts[view]}
                    rows={4}
                    maxLength={16000}
                  />
                  {view === 'review' && (
                    <label className="field-list">
                      Fields to extract <small>One field per line, up to 12</small>
                      <textarea
                        aria-label="Review fields"
                        value={fields}
                        onChange={(e) => setFields(e.target.value)}
                        rows={5}
                      />
                    </label>
                  )}
                  <div className="composer-footer">
                    <div>
                      {view === 'research' && (
                        <>
                          <label className="check">
                            <input type="checkbox" checked={cases} onChange={(e) => setCases(e.target.checked)} />
                            Case law
                          </label>
                          <label className="check">
                            <input
                              type="checkbox"
                              checked={searchOnly}
                              onChange={(e) => setSearchOnly(e.target.checked)}
                            />
                            Sources only
                          </label>
                        </>
                      )}
                    </div>
                    <Button
                      className="primary"
                      busy={busy || !!job}
                      disabled={view !== 'research' && view !== 'draft' && selected.length === 0}
                      onClick={run}
                    >
                      {job
                        ? 'Working…'
                        : result && view === 'research'
                          ? 'Ask follow-up'
                          : view === 'research'
                            ? 'Research'
                            : view === 'draft'
                              ? 'Create draft'
                              : 'Run review'}
                      <ArrowRight size={16} />
                    </Button>
                  </div>
                </section>
                {job && (
                  <div className="job-progress" role="status">
                    <Loader2 className="spin" size={18} />
                    <div>
                      <strong>
                        {job.status === 'pending' ? 'Queued for processing' : 'Working with your sources'}
                      </strong>
                      <small>You can leave this page. The job and result are saved.</small>
                    </div>
                    <button
                      onClick={async () => {
                        try {
                          await api(`/jobs/${job.job_id}`, 'DELETE');
                          setJob(null);
                        } catch (e) {
                          report(e);
                        }
                      }}
                    >
                      Cancel
                    </button>
                  </div>
                )}
                {result ? (
                  <ResultView
                    result={result}
                    setResult={setResult}
                    preview={(id) => setPreview(docs.find((d) => d.id === id) || null)}
                    report={report}
                  />
                ) : (
                  <div className="research-empty">
                    <div className="evidence-icon">
                      <Scale size={30} />
                    </div>
                    <h2>Keep the source in sight.</h2>
                    <p>
                      {view === 'research'
                        ? 'Choose documents and ask a focused question. Every retrieved passage stays available beside the answer.'
                        : 'Select your documents, add instructions, and run a review. Quotations are checked against the text supplied to the model.'}
                    </p>
                    <div className="empty-features">
                      <span>
                        <ShieldCheck size={15} />
                        Traceable evidence
                      </span>
                      <span>
                        <History size={15} />
                        Saved automatically
                      </span>
                    </div>
                  </div>
                )}
              </div>
              <aside className="source-panel card">
                <div className="composer-heading">
                  <span className="step-number">2</span>
                  <h3>Choose sources</h3>
                </div>
                <p className="muted">{activeMatter ? activeMatter.name : 'All accessible documents'}</p>
                <div className="source-tools">
                  <button onClick={() => setSelected(scoped.filter((d) => d.status === 'indexed').map((d) => d.id))}>
                    Select all
                  </button>
                  <button onClick={() => setSelected([])}>Clear</button>
                  <span>{selected.length} selected</span>
                </div>
                <div className="source-docs">
                  {scoped.length ? (
                    scoped.map((d) => (
                      <label className="source-doc" key={d.id}>
                        <input
                          type="checkbox"
                          checked={selected.includes(d.id)}
                          disabled={d.status !== 'indexed'}
                          onChange={(e) =>
                            setSelected((s) => (e.target.checked ? [...s, d.id] : s.filter((id) => id !== d.id)))
                          }
                        />
                        <FileText size={18} />
                        <span>
                          {d.filename}
                          <small>{d.status === 'indexed' ? formatSize(d.size) : d.status}</small>
                        </span>
                        <button
                          type="button"
                          className="icon"
                          aria-label={`Preview ${d.filename}`}
                          onClick={(e) => {
                            e.preventDefault();
                            setPreview(d);
                          }}
                        >
                          <ArrowUpRight size={14} />
                        </button>
                      </label>
                    ))
                  ) : (
                    <p className="muted source-placeholder">No documents in this scope yet.</p>
                  )}
                </div>
                <button className="upload-small" onClick={() => fileInput.current?.click()} disabled={uploading}>
                  <Upload size={16} />
                  {uploading ? 'Uploading…' : 'Upload documents'}
                </button>
                <p className="source-footnote">
                  Selected documents define the evidence for this request. Shared matter access is checked on every
                  request.
                </p>
              </aside>
            </div>
          )}
          <footer>
            CaseCite · GoodMem edition{' '}
            <span>AI output is a working draft. Verify sources and legal conclusions before use.</span>
          </footer>
        </div>
      </main>
      {preview && <DocumentPreview document={preview} close={() => setPreview(null)} />}
      {newMatter && (
        <Modal title="Create a matter" close={() => setNewMatter(false)}>
          <form
            onSubmit={async (e) => {
              e.preventDefault();
              try {
                const m = await api<Matter>('/matters', 'POST', Object.fromEntries(new FormData(e.currentTarget)));
                await refresh();
                setMatter(m.id);
                setSelected([]);
                setNewMatter(false);
              } catch (e) {
                report(e);
              }
            }}
          >
            <label>
              Matter name
              <input name="name" required maxLength={200} />
            </label>
            <label>
              Client name
              <input name="client_name" />
            </label>
            <Button className="primary">Create matter</Button>
          </form>
        </Modal>
      )}
      {members && activeMatter && (
        <MemberModal
          matter={activeMatter}
          close={() => setMembers(false)}
          onDelete={async () => {
            setMembers(false);
            setMatter('');
            await refresh();
          }}
        />
      )}
    </div>
  );
}

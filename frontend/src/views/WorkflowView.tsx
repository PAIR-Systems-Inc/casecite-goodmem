import { useEffect, useState } from 'react';
import { useParams } from 'react-router-dom';
import { useWorkflowsStore } from '../stores/workflowsStore';
import { useMattersStore } from '../stores/mattersStore';
import { appNavigate } from '../utils/router';
import {
  Icon,
  ReviewDocumentPicker,
  WorkflowStepList,
  WorkflowRunBar,
  WorkflowRunList,
  WorkflowStepRunCard,
} from '../components';
import type { WorkflowStep } from '../api/types';
import s from './WorkflowView.module.css';

/** One workflow: its steps (editable by the owner), the run bar, and the run history with per-step outcomes. */
function WorkflowView() {
  const { id } = useParams<{ id: string }>();
  const {
    workflow, workflowError, loadWorkflow,
    saveDetails, saveSteps, deleteWorkflow,
    runs, selectedRunId, selectedRun, selectRun,
    running, progress, runError, run, resume, cancel,
    editorOpen, setEditorOpen,
    pickerOpen, pickerDocs, pickerLoading, openPicker, closePicker,
  } = useWorkflowsStore();
  const activeMatter = useMattersStore((st) => st.activeMatter());
  const matters = useMattersStore((st) => st.matters);
  const mattersInit = useMattersStore((st) => st.init);

  useEffect(() => { mattersInit(); }, [mattersInit]);
  useEffect(() => { if (id) loadWorkflow(id); }, [id, loadWorkflow]);

  // Local UI state: the step draft while the editor is open, and the rename form.
  const [draftSteps, setDraftSteps] = useState<WorkflowStep[]>([]);
  const [renaming, setRenaming] = useState(false);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  useEffect(() => { if (editorOpen && workflow) setDraftSteps(workflow.steps); }, [editorOpen, workflow]);

  const isOwner = workflow?.role === 'owner';
  const workflowMatter = workflow?.matter_id ? matters.find((m) => m.id === workflow.matter_id) || null : null;
  const matterName = workflowMatter ? (workflowMatter.is_personal ? 'Personal' : workflowMatter.name) : null;

  const startRename = () => {
    if (!workflow) return;
    setName(workflow.name); setDescription(workflow.description || ''); setRenaming(true);
  };
  const submitRename = async (e: React.FormEvent) => {
    e.preventDefault();
    if (await saveDetails(name, description)) setRenaming(false);
  };
  const submitSteps = async () => {
    if (await saveSteps(draftSteps)) setEditorOpen(false);
  };

  if (workflowError) {
    return (
      <div className={s.container}>
        <div className={s.content}>
          <button className={s.backBtn} onClick={() => appNavigate('/workflows')}>
            <Icon name="ArrowLeft" size={14} /> All workflows
          </button>
          <div className={s.empty}><Icon name="AlertCircle" size={18} /><p>{workflowError}</p></div>
        </div>
      </div>
    );
  }

  if (!workflow) {
    return (
      <div className={s.container}>
        <div className={s.content}>
          <div className={s.empty}><Icon name="Loader2" size={16} className={s.spin} /> Loading workflow…</div>
        </div>
      </div>
    );
  }

  return (
    <div className={s.container}>
      <div className={s.content}>
        <button className={s.backBtn} onClick={() => appNavigate('/workflows')}>
          <Icon name="ArrowLeft" size={14} /> All workflows
        </button>

        <div className={s.headerRow}>
          {renaming ? (
            <form className={s.renameForm} onSubmit={submitRename}>
              <input className={s.input} value={name} onChange={(e) => setName(e.target.value)} aria-label="Workflow name" autoFocus />
              <input className={`${s.input} ${s.inputWide}`} value={description} onChange={(e) => setDescription(e.target.value)} placeholder="Description" aria-label="Description" />
              <button type="submit" className={s.primaryBtn} disabled={!name.trim()}><Icon name="Check" size={14} /> Save</button>
              <button type="button" className={s.secondaryBtn} onClick={() => setRenaming(false)}>Cancel</button>
            </form>
          ) : (
            <div className={s.titleBlock}>
              <div className={s.titleRow}>
                <h2 className={s.title}>{workflow.name}</h2>
                <span className={`${s.roleBadge} ${isOwner ? s.roleOwner : ''}`}>{workflow.role}</span>
                {workflow.template_key && <span className={s.roleBadge}>from template</span>}
                {isOwner && (
                  <button type="button" className={s.iconBtn} onClick={startRename} title="Rename" aria-label="Rename workflow">
                    <Icon name="Pencil" size={13} />
                  </button>
                )}
              </div>
              {workflow.description && <p className={s.subtitle}>{workflow.description}</p>}
              <div className={s.metaRow}>
                <span>{workflow.step_count} step{workflow.step_count === 1 ? '' : 's'}</span>
                <span>{workflow.run_count} run{workflow.run_count === 1 ? '' : 's'}</span>
                {matterName && <span>Filed under {matterName}</span>}
                {workflow.last_run_at && <span>Last run {new Date(workflow.last_run_at).toLocaleString()}</span>}
              </div>
            </div>
          )}
          <div className={s.headerActions}>
            <button className={`${s.secondaryBtn} ${editorOpen ? s.secondaryBtnActive : ''}`} onClick={() => setEditorOpen(!editorOpen)} disabled={!isOwner || running} title={isOwner ? undefined : 'Only the owner changes the steps'}>
              <Icon name="ListOrdered" size={14} /> Steps
            </button>
            {isOwner && (
              <button className={s.dangerBtn} onClick={deleteWorkflow} disabled={running} title="Delete this workflow">
                <Icon name="Trash2" size={14} />
              </button>
            )}
          </div>
        </div>

        {editorOpen ? (
          <div className={s.panel}>
            <div className={s.panelHead}>
              <span className={s.panelTitle}>Steps, in order</span>
              <span className={s.panelHint}>Past runs keep the steps they ran with.</span>
            </div>
            <WorkflowStepList steps={draftSteps} onChange={setDraftSteps} styles={s} />
            <div className={s.panelActions}>
              <button type="button" className={s.primaryBtn} onClick={submitSteps}><Icon name="Check" size={14} /> Save steps</button>
              <button type="button" className={s.secondaryBtn} onClick={() => setEditorOpen(false)}>Cancel</button>
            </div>
          </div>
        ) : (
          <ol className={s.stepSummary}>
            {workflow.steps.map((step, i) => (
              <li key={step.name} className={s.stepSummaryItem}>
                <span className={s.stepIndex}>{i + 1}</span>
                <span className={s.stepSummaryName}>{step.name}</span>
                <span className={s.muted}>{step.kind.replace(/_/g, ' ')}</span>
              </li>
            ))}
          </ol>
        )}

        <WorkflowRunBar
          stepCount={workflow.steps.length}
          matterName={matterName}
          running={running}
          progress={progress}
          runError={runError}
          canRun={workflow.steps.length > 0}
          onPickDocuments={openPicker}
          onRunOnMatter={() => run([])}
          onCancel={cancel}
          styles={s}
        />

        <div className={s.runWorkspace}>
          <div className={s.runHistory}>
            <div className={s.sectionLabel}>Runs</div>
            <WorkflowRunList
              runs={runs}
              selectedRunId={selectedRunId}
              running={running}
              onSelect={(runId) => void selectRun(runId)}
              onResume={resume}
              styles={s}
            />
          </div>
          <div className={s.runDetail}>
            {selectedRun ? (
              <>
                <div className={s.sectionLabel}>
                  Run of {new Date(selectedRun.created_at).toLocaleString()} · {selectedRun.status}
                  {selectedRun.documents ? ` · ${selectedRun.documents} document${selectedRun.documents === 1 ? '' : 's'}` : ''}
                </div>
                {selectedRun.error && <div className={s.runErrorBox}><Icon name="AlertTriangle" size={14} /> {selectedRun.error}</div>}
                {selectedRun.step_runs.map((step) => (
                  <WorkflowStepRunCard key={step.index} step={step} styles={s} />
                ))}
              </>
            ) : (
              <div className={s.empty}>
                <Icon name="Workflow" size={18} />
                <p>Run the workflow to see each step&apos;s outcome here.</p>
              </div>
            )}
          </div>
        </div>

        <ReviewDocumentPicker
          isOpen={pickerOpen}
          documents={pickerDocs}
          loading={pickerLoading}
          selectedIds={[]}
          scopeLabel={activeMatter
            ? `Documents filed under "${activeMatter.is_personal ? 'Personal' : activeMatter.name}"`
            : 'Every indexed document you can read. Set an active matter to narrow this list.'}
          onConfirm={async (ids) => { await run(ids); return true; }}
          onClose={closePicker}
          styles={s}
        />
      </div>
    </div>
  );
}

export default WorkflowView;

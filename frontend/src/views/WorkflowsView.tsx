import { useEffect, useState } from 'react';
import { useWorkflowsStore, newStep } from '../stores/workflowsStore';
import { useMattersStore } from '../stores/mattersStore';
import { Icon, WorkflowCard, WorkflowTemplateGallery, WorkflowStepList } from '../components';
import type { WorkflowStep } from '../api/types';
import s from './WorkflowsView.module.css';

/** The firm's workflows: open one, start from a template, or build a new step sequence. */
function WorkflowsView() {
  const {
    workflows, workflowsLoading, templates, init, loadWorkflows, createWorkflow, createFromTemplate, openWorkflow,
  } = useWorkflowsStore();
  const activeMatter = useMattersStore((st) => st.activeMatter());
  const mattersInit = useMattersStore((st) => st.init);
  useEffect(() => { init(); mattersInit(); }, [init, mattersInit]);

  // Local UI state only: the create form and which template is being instantiated.
  const [formOpen, setFormOpen] = useState(false);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [steps, setSteps] = useState<WorkflowStep[]>(() => [newStep('extract_fields', [])]);
  const [creating, setCreating] = useState(false);
  const [templateBusy, setTemplateBusy] = useState<string | null>(null);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setCreating(true);
    const workflow = await createWorkflow({ name, description, steps });
    setCreating(false);
    if (workflow) {
      setName(''); setDescription(''); setSteps([newStep('extract_fields', [])]); setFormOpen(false);
      openWorkflow(workflow.id);
    }
  };

  const useTemplate = async (key: string) => {
    setTemplateBusy(key);
    const workflow = await createFromTemplate(key);
    setTemplateBusy(null);
    if (workflow) openWorkflow(workflow.id);
  };

  const scopeLabel = activeMatter ? (activeMatter.is_personal ? 'Personal' : activeMatter.name) : null;

  return (
    <div className={s.container}>
      <div className={s.content}>
        <div className={s.headerRow}>
          <div>
            <h2 className={s.title}>Workflows</h2>
            <p className={s.subtitle}>
              Save a review, research or drafting process as an ordered list of steps, then run it over any set of
              documents. Every step&apos;s output is kept, and a run that stops can be resumed from the step that failed.
            </p>
          </div>
          <div className={s.headerActions}>
            <button className={s.secondaryBtn} onClick={loadWorkflows} disabled={workflowsLoading}>
              <Icon name={workflowsLoading ? 'Loader2' : 'RefreshCw'} size={14} className={workflowsLoading ? s.spin : undefined} />
              Refresh
            </button>
            <button className={s.primaryBtn} onClick={() => setFormOpen((o) => !o)}>
              <Icon name="Plus" size={14} /> New workflow
            </button>
          </div>
        </div>

        {formOpen && (
          <form className={s.createForm} onSubmit={submit}>
            <div className={s.formRow}>
              <input
                className={s.input}
                placeholder="Workflow name, e.g. Vendor contract intake"
                value={name}
                onChange={(e) => setName(e.target.value)}
                aria-label="Workflow name"
                autoFocus
              />
              <input
                className={`${s.input} ${s.inputWide}`}
                placeholder="What this workflow is for (optional)"
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                aria-label="Description"
              />
            </div>
            <div className={s.formLabel}>Steps, in order</div>
            <WorkflowStepList steps={steps} onChange={setSteps} styles={s} />
            <div className={s.formNote}>
              <Icon name={scopeLabel ? 'Target' : 'Info'} size={13} />
              {scopeLabel
                ? `Filed under "${scopeLabel}": members of that matter can see and run it, and "Run on matter" uses its documents.`
                : 'Private to you. Set an active matter in the header to share a workflow with the matter’s members.'}
            </div>
            <div className={s.formActions}>
              <button type="submit" className={s.primaryBtn} disabled={creating || !name.trim() || steps.length === 0}>
                <Icon name={creating ? 'Loader2' : 'Check'} size={14} className={creating ? s.spin : undefined} />
                Create workflow
              </button>
              <button type="button" className={s.secondaryBtn} onClick={() => setFormOpen(false)}>Cancel</button>
            </div>
          </form>
        )}

        {templates.length > 0 && (
          <>
            <div className={s.sectionLabel}>Start from a template</div>
            <WorkflowTemplateGallery templates={templates} busyKey={templateBusy} onUse={useTemplate} styles={s} />
          </>
        )}

        <div className={s.sectionLabel}>Your workflows</div>
        {workflowsLoading && workflows.length === 0 ? (
          <div className={s.empty}><Icon name="Loader2" size={16} className={s.spin} /> Loading workflows…</div>
        ) : workflows.length === 0 ? (
          <div className={s.empty}>
            <Icon name="Workflow" size={18} />
            <p>No workflows yet. Start from a template above, or build one step by step.</p>
          </div>
        ) : (
          <div className={s.grid}>
            {workflows.map((w) => (
              <WorkflowCard key={w.id} workflow={w} onOpen={openWorkflow} styles={s} />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

export default WorkflowsView;

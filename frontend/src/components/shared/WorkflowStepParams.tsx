import { ReviewSchemaEditor } from './ReviewSchemaEditor';
import type { DeepResearchTool, ReviewFieldSpec, WorkflowStep } from '../../api/types';

interface WorkflowStepParamsProps {
  step: WorkflowStep;
  /** Names of the steps before this one: the only ones it may refer to. */
  earlierSteps: string[];
  disabled?: boolean;
  onChange: (params: Record<string, unknown>) => void;
  styles: Record<string, string>;
}

const TOOLS: Array<{ value: DeepResearchTool; label: string }> = [
  { value: 'documents', label: 'Firm documents' },
  { value: 'case_law', label: 'Case law' },
];

const str = (v: unknown) => (typeof v === 'string' ? v : '');
const list = (v: unknown): string[] => (Array.isArray(v) ? v.map(String) : []);

/** The parameter form for one step, by kind. Nothing here is code: every field maps to a validated server parameter. */
export function WorkflowStepParams({ step, earlierSteps, disabled, onChange, styles }: WorkflowStepParamsProps) {
  const p = step.params || {};
  const set = (patch: Record<string, unknown>) => onChange({ ...p, ...patch });
  const toggleIn = (key: string, name: string) => {
    const current = list(p[key]);
    set({ [key]: current.includes(name) ? current.filter((n) => n !== name) : [...current, name] });
  };

  const stepPicker = (key: string, label: string, hint: string) => (
    <div className={styles.paramRow}>
      <span className={styles.paramLabel}>{label}</span>
      {earlierSteps.length === 0 ? (
        <span className={styles.paramHint}>{hint}</span>
      ) : (
        <div className={styles.checkRow}>
          {earlierSteps.map((name) => (
            <label key={name} className={styles.checkLabel}>
              <input
                type="checkbox"
                checked={list(p[key]).includes(name)}
                onChange={() => toggleIn(key, name)}
                disabled={disabled}
              />
              {name}
            </label>
          ))}
        </div>
      )}
    </div>
  );

  switch (step.kind) {
    case 'extract_fields':
      return (
        <div className={styles.params}>
          <ReviewSchemaEditor
            fields={(p.fields as ReviewFieldSpec[]) || []}
            onChange={(fields) => set({ fields })}
            disabled={disabled}
            styles={styles}
          />
          <input
            className={styles.input}
            placeholder="Name for the review table it creates (optional)"
            value={str(p.table_name)}
            onChange={(e) => set({ table_name: e.target.value })}
            disabled={disabled}
            aria-label="Review table name"
          />
        </div>
      );
    case 'analyze_contract':
      return (
        <div className={styles.params}>
          <div className={styles.paramRow}>
            <input className={styles.input} placeholder="Contract type (auto)" value={str(p.contract_type)} onChange={(e) => set({ contract_type: e.target.value || 'auto' })} disabled={disabled} aria-label="Contract type" />
            <input className={styles.input} placeholder="Jurisdiction (optional)" value={str(p.jurisdiction)} onChange={(e) => set({ jurisdiction: e.target.value })} disabled={disabled} aria-label="Jurisdiction" />
            <input className={styles.input} placeholder="Representing (optional)" value={str(p.representing)} onChange={(e) => set({ representing: e.target.value })} disabled={disabled} aria-label="Representing" />
            <select className={styles.select} value={str(p.posture) || 'balanced'} onChange={(e) => set({ posture: e.target.value })} disabled={disabled} aria-label="Posture">
              <option value="balanced">Balanced</option>
              <option value="strict">Strict</option>
            </select>
          </div>
          <textarea className={styles.textarea} placeholder="Standing instructions for every review (optional)" value={str(p.instructions)} onChange={(e) => set({ instructions: e.target.value })} disabled={disabled} rows={2} aria-label="Review instructions" />
        </div>
      );
    case 'compare_contracts':
      return (
        <div className={styles.params}>
          <div className={styles.paramRow}>
            <input className={styles.input} placeholder="Baseline document id (defaults to the first input)" value={str(p.baseline_document_id)} onChange={(e) => set({ baseline_document_id: e.target.value })} disabled={disabled} aria-label="Baseline document id" />
            <input className={`${styles.input} ${styles.inputWide}`} placeholder="Focus, e.g. payment terms and liability (optional)" value={str(p.focus)} onChange={(e) => set({ focus: e.target.value })} disabled={disabled} aria-label="Comparison focus" />
          </div>
          <span className={styles.paramHint}>Up to three documents are compared against the baseline.</span>
        </div>
      );
    case 'draft_document':
      return (
        <div className={styles.params}>
          <textarea
            className={styles.textarea}
            placeholder="Drafting instructions. Use {{step.summary}} or {{step.data.field}} to pull in earlier outputs."
            value={str(p.instructions)}
            onChange={(e) => set({ instructions: e.target.value })}
            disabled={disabled}
            rows={4}
            aria-label="Drafting instructions"
          />
          {stepPicker('include_outputs_from', 'Attach outputs of', 'No earlier steps yet. Move this step down to attach their outputs.')}
        </div>
      );
    case 'deep_research':
      return (
        <div className={styles.params}>
          <textarea
            className={styles.textarea}
            placeholder="Research question. Use {{step.data.values}} and similar to include earlier outputs."
            value={str(p.question)}
            onChange={(e) => set({ question: e.target.value })}
            disabled={disabled}
            rows={3}
            aria-label="Research question"
          />
          <div className={styles.paramRow}>
            <span className={styles.paramLabel}>Sources</span>
            <div className={styles.checkRow}>
              {TOOLS.map((t) => {
                const tools = list(p.tools);
                const checked = tools.length === 0 || tools.includes(t.value);
                return (
                  <label key={t.value} className={styles.checkLabel}>
                    <input
                      type="checkbox"
                      checked={checked}
                      onChange={() => {
                        const next = checked ? TOOLS.map((x) => x.value).filter((x) => x !== t.value) : [...tools, t.value];
                        set({ tools: next.length === 0 || next.length === TOOLS.length ? null : next });
                      }}
                      disabled={disabled}
                    />
                    {t.label}
                  </label>
                );
              })}
            </div>
          </div>
        </div>
      );
    case 'citation_check':
      return (
        <div className={styles.params}>
          <textarea
            className={styles.textarea}
            placeholder="Citations to check, one per line (optional)"
            value={list(p.citations).join('\n')}
            onChange={(e) => set({ citations: e.target.value.split('\n').map((c) => c.trim()).filter(Boolean) })}
            disabled={disabled}
            rows={2}
            aria-label="Citations"
          />
          <div className={styles.paramRow}>
            <span className={styles.paramLabel}>Also scan outputs of</span>
            <label className={styles.checkLabel}>
              <input
                type="checkbox"
                checked={p.from_steps === null || p.from_steps === undefined}
                onChange={(e) => set({ from_steps: e.target.checked ? null : [] })}
                disabled={disabled}
              />
              every earlier step
            </label>
            {Array.isArray(p.from_steps) && earlierSteps.map((name) => (
              <label key={name} className={styles.checkLabel}>
                <input type="checkbox" checked={list(p.from_steps).includes(name)} onChange={() => toggleIn('from_steps', name)} disabled={disabled} />
                {name}
              </label>
            ))}
          </div>
        </div>
      );
    default:
      return null;
  }
}

import { Icon } from './Icon';
import type { DeepResearchSubQuestion, DeepResearchTool } from '../../api/types';

interface DeepResearchPlanEditorProps {
  plan: DeepResearchSubQuestion[];
  caps: Record<string, number>;
  disabled: boolean;
  onChange: (id: string, patch: { question?: string; tools?: DeepResearchTool[] }) => void;
  onRemove: (id: string) => void;
  onAdd: () => void;
  styles: Record<string, string>;
}

const TOOL_LABEL: Record<DeepResearchTool, string> = {
  documents: 'Firm documents',
  case_law: 'Case law',
};

/** The proposed sub-questions, each editable, removable and re-targetable before the run. */
export function DeepResearchPlanEditor({ plan, caps, disabled, onChange, onRemove, onAdd, styles }: DeepResearchPlanEditorProps) {
  const max = caps.max_subquestions ?? 8;

  const toggleTool = (step: DeepResearchSubQuestion, tool: DeepResearchTool) => {
    const has = step.tools.includes(tool);
    const tools = has ? step.tools.filter((t) => t !== tool) : [...step.tools, tool];
    if (tools.length === 0) return; // a step needs at least one tool
    onChange(step.id, { tools });
  };

  return (
    <div className={styles.plan}>
      <div className={styles.planHead}>
        <span className={styles.planTitle}>Plan</span>
        <span className={styles.muted}>Edit the sub-questions and which sources each may use, then run.</span>
      </div>
      <ol className={styles.planList}>
        {plan.map((step, index) => (
          <li key={step.id} className={styles.planStep}>
            <span className={styles.planIndex}>{index + 1}</span>
            <div className={styles.planBody}>
              <input
                type="text"
                className={styles.planInput}
                value={step.question}
                onChange={(e) => onChange(step.id, { question: e.target.value })}
                placeholder="Sub-question"
                disabled={disabled}
                aria-label={`Sub-question ${index + 1}`}
              />
              <div className={styles.planTools}>
                {(Object.keys(TOOL_LABEL) as DeepResearchTool[]).map((tool) => (
                  <label key={tool} className={styles.toolCheck}>
                    <input
                      type="checkbox"
                      checked={step.tools.includes(tool)}
                      onChange={() => toggleTool(step, tool)}
                      disabled={disabled}
                    />
                    {TOOL_LABEL[tool]}
                  </label>
                ))}
              </div>
            </div>
            <button
              type="button"
              className={styles.iconBtn}
              onClick={() => onRemove(step.id)}
              disabled={disabled || plan.length === 1}
              aria-label={`Remove sub-question ${index + 1}`}
              title="Remove"
            >
              <Icon name="Trash2" size={14} />
            </button>
          </li>
        ))}
      </ol>
      <button type="button" className={styles.linkBtn} onClick={onAdd} disabled={disabled || plan.length >= max}>
        <Icon name="Plus" size={13} /> Add a sub-question ({plan.length}/{max})
      </button>
    </div>
  );
}

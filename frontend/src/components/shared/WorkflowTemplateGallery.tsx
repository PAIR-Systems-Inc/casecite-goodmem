import { Icon } from './Icon';
import { stepKindInfo } from '../../stores/workflowsStore';
import type { WorkflowTemplate } from '../../api/types';

interface WorkflowTemplateGalleryProps {
  templates: WorkflowTemplate[];
  busyKey: string | null;
  onUse: (key: string) => void;
  styles: Record<string, string>;
}

/** The built-in starter workflows: one card each, with the steps it would create. */
export function WorkflowTemplateGallery({ templates, busyKey, onUse, styles }: WorkflowTemplateGalleryProps) {
  if (templates.length === 0) return null;
  return (
    <div className={styles.templateGrid}>
      {templates.map((t) => (
        <div key={t.key} className={styles.templateCard}>
          <div className={styles.templateHead}>
            <Icon name="Sparkles" size={14} />
            <span className={styles.templateTitle}>{t.name}</span>
          </div>
          <p className={styles.templateDesc}>{t.description}</p>
          <ol className={styles.templateSteps}>
            {t.steps.map((s) => {
              const info = stepKindInfo(s.kind);
              return (
                <li key={s.name}>
                  <Icon name={info.icon} size={12} /> {info.title}
                </li>
              );
            })}
          </ol>
          <button
            type="button"
            className={styles.secondaryBtn}
            onClick={() => onUse(t.key)}
            disabled={busyKey !== null}
          >
            <Icon name={busyKey === t.key ? 'Loader2' : 'Plus'} size={14} className={busyKey === t.key ? styles.spin : undefined} />
            Use template
          </button>
        </div>
      ))}
    </div>
  );
}

import { Icon } from './Icon';
import type { ReviewFieldSpec, ReviewFieldType } from '../../api/types';

interface ReviewSchemaEditorProps {
  fields: ReviewFieldSpec[];
  onChange: (fields: ReviewFieldSpec[]) => void;
  /** Offered when the list is empty, or as "add" when it is not. */
  onAddStandardFields?: () => void;
  disabled?: boolean;
  styles: Record<string, string>;
}

const FIELD_TYPES: Array<{ value: ReviewFieldType; label: string }> = [
  { value: 'text', label: 'Text' },
  { value: 'date', label: 'Date' },
  { value: 'money', label: 'Money' },
  { value: 'number', label: 'Number' },
  { value: 'boolean', label: 'Yes / no' },
  { value: 'duration', label: 'Duration' },
];

/** The columns of a review table: a name, a type and an optional hint for the model. */
export function ReviewSchemaEditor({ fields, onChange, onAddStandardFields, disabled, styles }: ReviewSchemaEditorProps) {
  const update = (index: number, patch: Partial<ReviewFieldSpec>) =>
    onChange(fields.map((f, i) => (i === index ? { ...f, ...patch } : f)));
  const remove = (index: number) => onChange(fields.filter((_f, i) => i !== index));
  const add = () => onChange([...fields, { name: '', description: '', type: 'text' }]);

  return (
    <div className={styles.schema}>
      {fields.length === 0 && (
        <div className={styles.schemaEmpty}>
          No fields yet. Add the facts you want pulled out of every document — each becomes a column.
        </div>
      )}
      {fields.map((field, index) => (
        <div key={field.key || `new-${index}`} className={styles.schemaRow}>
          <input
            className={styles.input}
            placeholder="Field name, e.g. Governing law"
            value={field.name}
            onChange={(e) => update(index, { name: e.target.value })}
            disabled={disabled}
            aria-label={`Field ${index + 1} name`}
          />
          <select
            className={styles.select}
            value={field.type}
            onChange={(e) => update(index, { type: e.target.value as ReviewFieldType })}
            disabled={disabled}
            aria-label={`Field ${index + 1} type`}
          >
            {FIELD_TYPES.map((t) => (
              <option key={t.value} value={t.value}>{t.label}</option>
            ))}
          </select>
          <input
            className={`${styles.input} ${styles.inputWide}`}
            placeholder="What to look for (optional hint for the model)"
            value={field.description || ''}
            onChange={(e) => update(index, { description: e.target.value })}
            disabled={disabled}
            aria-label={`Field ${index + 1} description`}
          />
          <button
            type="button"
            className={styles.iconBtn}
            onClick={() => remove(index)}
            disabled={disabled}
            title="Remove field"
            aria-label={`Remove field ${index + 1}`}
          >
            <Icon name="X" size={14} />
          </button>
        </div>
      ))}
      <div className={styles.schemaActions}>
        <button type="button" className={styles.secondaryBtn} onClick={add} disabled={disabled}>
          <Icon name="Plus" size={14} /> Add field
        </button>
        {onAddStandardFields && (
          <button type="button" className={styles.secondaryBtn} onClick={onAddStandardFields} disabled={disabled}>
            <Icon name="Sparkles" size={14} /> Add standard contract fields
          </button>
        )}
      </div>
    </div>
  );
}

import { useEffect, useRef, type ReactNode } from 'react';
import { X, Loader2 } from 'lucide-react';

export function Button({
  children,
  busy,
  ...props
}: React.ButtonHTMLAttributes<HTMLButtonElement> & { busy?: boolean }) {
  return (
    <button {...props} disabled={props.disabled || busy}>
      {busy && <Loader2 size={16} className="spin" />}
      {children}
    </button>
  );
}

export function Modal({ title, children, close }: { title: string; children: ReactNode; close: () => void }) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    ref.current?.showModal();
  }, []);
  return (
    <dialog ref={ref} onCancel={close}>
      <div className="modal-head">
        <h2>{title}</h2>
        <button className="icon" aria-label="Close" onClick={close}>
          <X />
        </button>
      </div>
      {children}
    </dialog>
  );
}

export function formatSize(bytes: number) {
  return bytes < 1024 * 1024 ? `${Math.ceil(bytes / 1024)} KB` : `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

export function Empty({
  icon,
  title,
  text,
  action,
}: {
  icon: ReactNode;
  title: string;
  text: string;
  action?: ReactNode;
}) {
  return (
    <div className="empty">
      <div className="evidence-icon">{icon}</div>
      <h2>{title}</h2>
      <p>{text}</p>
      {action}
    </div>
  );
}

export function Readable({ text }: { text: string }) {
  return (
    <div className="prose">
      {text
        .split('\n')
        .map((line, i) =>
          /^#{1,4} /.test(line) ? (
            <h3 key={i}>{line.replace(/^#+ /, '')}</h3>
          ) : line ? (
            <p key={i}>{line.replace(/\*\*([^*]+)\*\*/g, '$1')}</p>
          ) : (
            <div key={i} className="paragraph-gap" />
          ),
        )}
    </div>
  );
}

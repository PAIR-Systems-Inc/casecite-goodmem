import { useEffect, useState } from 'react';
import { FileText, Download, ChevronLeft, ChevronRight } from 'lucide-react';
import { api, url, errorMessage, type Document } from './api';
import { Modal, Empty } from './ui';

export function DocumentPreview({ document: d, close }: { document: Document; close: () => void }) {
  const [pages, setPages] = useState<{ pageIndex: number; dpi?: number; contentType?: string }[]>([]);
  const [index, setIndex] = useState(0);
  const [text, setText] = useState('');
  const [mode, setMode] = useState<'pages' | 'text'>('pages');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);
  useEffect(() => {
    let active = true;
    api<{ pages: typeof pages }>(`/documents/${d.id}/pages`)
      .then((r) => {
        if (active) setPages(r.pages);
      })
      .catch((e) => {
        if (active) setError(errorMessage(e));
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [d.id]);
  async function loadText() {
    setMode('text');
    setError('');
    if (text) return;
    setLoading(true);
    try {
      const r = await api<{ text: string }>(`/documents/${d.id}/content`);
      setText(r.text || 'No extractable text. Use page images or download the original.');
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setLoading(false);
    }
  }
  const page = pages[index];
  return (
    <Modal title={d.filename} close={close}>
      <div className="preview-tools">
        <div className="segmented">
          <button className={mode === 'pages' ? 'chosen' : ''} onClick={() => setMode('pages')}>
            Page images
          </button>
          <button className={mode === 'text' ? 'chosen' : ''} onClick={loadText}>
            Text
          </button>
        </div>
        <a href={url(`/documents/${d.id}/file`)}>
          <Download size={15} />
          Original file
        </a>
      </div>
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      {loading ? (
        <p className="muted">Loading document…</p>
      ) : mode === 'text' ? (
        <pre className="document-text">{text}</pre>
      ) : pages.length ? (
        <>
          <div className="page-nav">
            <button
              className="icon"
              aria-label="Previous page"
              disabled={index === 0}
              onClick={() => setIndex((i) => i - 1)}
            >
              <ChevronLeft />
            </button>
            <span>
              Page {page.pageIndex + 1} of {pages.length}
            </span>
            <button
              className="icon"
              aria-label="Next page"
              disabled={index === pages.length - 1}
              onClick={() => setIndex((i) => i + 1)}
            >
              <ChevronRight />
            </button>
          </div>
          <div className="page-image">
            <img
              src={url(`/documents/${d.id}/pages/${page.pageIndex}/image${page.dpi ? '?dpi=' + page.dpi : ''}`)}
              alt={`Page ${page.pageIndex + 1} of ${d.filename}`}
              onError={() => setError('This page image could not be loaded.')}
            />
          </div>
        </>
      ) : (
        <Empty
          icon={<FileText size={30} />}
          title="No page images available"
          text="Images may still be processing or may not be supported for this format. Open the text or download the original file."
        />
      )}
    </Modal>
  );
}

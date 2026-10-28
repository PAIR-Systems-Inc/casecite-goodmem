import { useUIStore } from './uiStore';
import type { ImportFileError } from '../api/types';

/** Show a toast from store code (outside React). */
export function toast(msg: string, type = 'info'): void {
  try { useUIStore.getState().addToast(msg, type); } catch { /* noop */ }
}

/** How many per-file failures a toast names before "and N more". */
const MAX_NAMED_FILE_ERRORS = 3;

/**
 * One line for a toast about per-file failures from a sync or picker import:
 * `lead: a.pdf: too large; b.docx: unsupported type; and 4 more`. `total` is
 * the complete failure count (the server lists at most the first 20).
 */
export function describeFileErrors(lead: string, errors: ImportFileError[] | undefined | null, total: number): string {
  const named = (errors || [])
    .filter((e) => e && (e.name || e.reason))
    .slice(0, MAX_NAMED_FILE_ERRORS)
    .map((e) => (e.name && e.reason ? `${e.name}: ${e.reason}` : e.name || e.reason));
  if (named.length === 0) return lead;
  const more = Math.max(total, errors?.length || 0) - named.length;
  return `${lead}: ${named.join('; ')}${more > 0 ? `; and ${more} more` : ''}`;
}

/** True for an API error thrown with HTTP status 429 (see api.request). */
export function isRateLimited(error: unknown): boolean {
  return !!error && typeof error === 'object' && (error as { status?: unknown }).status === 429;
}

interface PollableJob {
  status: string; // pending | processing | completed | failed | cancelled
  error?: string | null;
}

export interface JobPollOptions<J extends PollableJob> {
  fetchJob: () => Promise<J>;
  /** The job reached `completed`. */
  onCompleted: (job: J) => void;
  /** The job failed or was cancelled, the wait budget ran out, or polling itself threw. */
  onFailed: (message: string) => void;
  /** Called on every poll that finds the job still running. */
  onPending?: (job: J, attempt: number) => void;
  /** Give up after this many polls. */
  maxAttempts: number;
  intervalMs?: number;
  failedMessage: string;
  timeoutMessage: string;
}

const DEFAULT_POLL_INTERVAL_MS = 3000;
/** A 429 from /jobs is the poller itself being throttled, not the job failing. */
const RATE_LIMIT_BACKOFF_MS = 5000;
/** Consecutive status-request failures tolerated before the job is given up. */
const MAX_TRANSIENT_FAILURES = 3;

/**
 * Poll a background job until it settles. One poller runs at most one poll
 * loop: `start` supersedes any earlier loop and `stop` cancels it, including
 * a status request that is already in flight — its answer is discarded, so a
 * stopped (or reset) store is never written to by a stale job.
 */
export function createJobPoller() {
  let timer: ReturnType<typeof setTimeout> | null = null;
  let generation = 0;

  const stop = () => {
    generation += 1;
    if (timer) { clearTimeout(timer); timer = null; }
  };

  const start = <J extends PollableJob>(opts: JobPollOptions<J>) => {
    stop();
    const mine = generation;
    const intervalMs = opts.intervalMs ?? DEFAULT_POLL_INTERVAL_MS;
    let transientFailures = 0;
    const poll = async (attempt: number) => {
      try {
        const job = await opts.fetchJob();
        if (mine !== generation) return;
        transientFailures = 0;
        if (job.status === 'completed') { opts.onCompleted(job); return; }
        if (job.status === 'failed' || job.status === 'cancelled') {
          opts.onFailed(job.error || opts.failedMessage);
          return;
        }
        if (attempt > opts.maxAttempts) { opts.onFailed(opts.timeoutMessage); return; }
        opts.onPending?.(job, attempt);
        timer = setTimeout(() => poll(attempt + 1), intervalMs);
      } catch (e) {
        if (mine !== generation) return;
        if (attempt > opts.maxAttempts) { opts.onFailed(opts.timeoutMessage); return; }
        if (isRateLimited(e)) {
          // Throttled: wait it out and keep polling — the job is still running.
          timer = setTimeout(() => poll(attempt + 1), RATE_LIMIT_BACKOFF_MS);
          return;
        }
        transientFailures += 1;
        if (transientFailures <= MAX_TRANSIENT_FAILURES) {
          timer = setTimeout(() => poll(attempt + 1), intervalMs);
          return;
        }
        opts.onFailed(e instanceof Error ? e.message : 'Polling failed.');
      }
    };
    poll(0);
  };

  return { start, stop };
}

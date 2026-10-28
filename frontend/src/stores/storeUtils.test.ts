import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { createJobPoller, describeFileErrors } from './storeUtils';

type Job = { status: string; error?: string | null; result?: string };

function options(fetchJob: () => Promise<Job>) {
  return {
    fetchJob,
    maxAttempts: 3,
    intervalMs: 1000,
    failedMessage: 'Job failed.',
    timeoutMessage: 'Timed out.',
    onCompleted: vi.fn(),
    onFailed: vi.fn(),
    onPending: vi.fn(),
  };
}

describe('createJobPoller', () => {
  beforeEach(() => { vi.useFakeTimers(); });
  afterEach(() => { vi.useRealTimers(); });

  it('polls until the job completes', async () => {
    const fetchJob = vi.fn<() => Promise<Job>>()
      .mockResolvedValueOnce({ status: 'processing' })
      .mockResolvedValueOnce({ status: 'completed', result: 'done' });
    const opts = options(fetchJob);

    createJobPoller().start(opts);
    await vi.advanceTimersByTimeAsync(0);
    expect(opts.onPending).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1000);

    expect(opts.onCompleted).toHaveBeenCalledWith({ status: 'completed', result: 'done' });
    expect(opts.onFailed).not.toHaveBeenCalled();
  });

  it('reports a failed job with the server message, or the fallback', async () => {
    const withMessage = options(vi.fn().mockResolvedValue({ status: 'failed', error: 'Out of quota' }));
    createJobPoller().start(withMessage);
    await vi.advanceTimersByTimeAsync(0);
    expect(withMessage.onFailed).toHaveBeenCalledWith('Out of quota');

    const cancelled = options(vi.fn().mockResolvedValue({ status: 'cancelled' }));
    createJobPoller().start(cancelled);
    await vi.advanceTimersByTimeAsync(0);
    expect(cancelled.onFailed).toHaveBeenCalledWith('Job failed.');
  });

  it('gives up after the attempt budget', async () => {
    const opts = options(vi.fn().mockResolvedValue({ status: 'processing' }));
    createJobPoller().start(opts);
    await vi.advanceTimersByTimeAsync(10_000);
    expect(opts.onFailed).toHaveBeenCalledWith('Timed out.');
    expect(opts.onCompleted).not.toHaveBeenCalled();
  });

  it('retries a transient polling error a few times before giving up', async () => {
    const opts = options(vi.fn().mockRejectedValue(new Error('Network down')));
    opts.maxAttempts = 50;
    createJobPoller().start(opts);
    await vi.advanceTimersByTimeAsync(0);
    // One blip is not a failed job.
    expect(opts.onFailed).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(3000);
    expect(opts.onFailed).toHaveBeenCalledWith('Network down');
    expect(opts.onFailed).toHaveBeenCalledTimes(1);
  });

  it('recovers when a status request succeeds after a blip', async () => {
    const fetchJob = vi.fn<() => Promise<Job>>()
      .mockRejectedValueOnce(new Error('Network down'))
      .mockResolvedValueOnce({ status: 'completed', result: 'done' });
    const opts = options(fetchJob);
    createJobPoller().start(opts);
    await vi.advanceTimersByTimeAsync(1000);
    expect(opts.onCompleted).toHaveBeenCalledWith({ status: 'completed', result: 'done' });
    expect(opts.onFailed).not.toHaveBeenCalled();
  });

  it('backs off 5 s on a 429 and keeps polling instead of failing the job', async () => {
    const throttled = Object.assign(new Error('Rate limit exceeded'), { status: 429 });
    const fetchJob = vi.fn<() => Promise<Job>>()
      .mockRejectedValueOnce(throttled)
      .mockRejectedValueOnce(throttled)
      .mockResolvedValueOnce({ status: 'completed', result: 'done' });
    const opts = options(fetchJob);
    opts.maxAttempts = 50;
    createJobPoller().start(opts);

    await vi.advanceTimersByTimeAsync(0);
    expect(opts.onFailed).not.toHaveBeenCalled();
    // Not retried at the normal interval…
    await vi.advanceTimersByTimeAsync(4000);
    expect(fetchJob).toHaveBeenCalledTimes(1);
    // …but after the back-off, and again after the second 429.
    await vi.advanceTimersByTimeAsync(1000);
    expect(fetchJob).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(5000);
    expect(fetchJob).toHaveBeenCalledTimes(3);
    expect(opts.onCompleted).toHaveBeenCalledWith({ status: 'completed', result: 'done' });
    expect(opts.onFailed).not.toHaveBeenCalled();
  });

  it('polls every 3 s by default', async () => {
    const fetchJob = vi.fn<() => Promise<Job>>().mockResolvedValue({ status: 'processing' });
    const { intervalMs: _ignored, ...opts } = options(fetchJob);
    createJobPoller().start({ ...opts, maxAttempts: 50 });
    await vi.advanceTimersByTimeAsync(0);
    expect(fetchJob).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(2999);
    expect(fetchJob).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(fetchJob).toHaveBeenCalledTimes(2);
  });

  it('stop() discards the answer of a status request already in flight', async () => {
    let resolve!: (job: Job) => void;
    const opts = options(vi.fn(() => new Promise<Job>((r) => { resolve = r; })));
    const poller = createJobPoller();
    poller.start(opts);

    poller.stop();
    resolve({ status: 'completed', result: 'stale' });
    await vi.advanceTimersByTimeAsync(5000);

    expect(opts.onCompleted).not.toHaveBeenCalled();
    expect(opts.onFailed).not.toHaveBeenCalled();
  });
});

describe('describeFileErrors', () => {
  it('names up to three files with their reasons, then counts the rest', () => {
    const errors = [
      { name: 'a.pdf', reason: 'too large' },
      { name: 'b.docx', reason: 'unsupported type' },
      { name: 'c.pdf', reason: 'download failed' },
      { name: 'd.pdf', reason: 'download failed' },
    ];
    expect(describeFileErrors('Failed to import 6 file(s)', errors, 6)).toBe(
      'Failed to import 6 file(s): a.pdf: too large; b.docx: unsupported type; c.pdf: download failed; and 3 more',
    );
  });

  it('falls back to the lead line when the server listed nothing', () => {
    expect(describeFileErrors('Failed to import 2 file(s)', [], 2)).toBe('Failed to import 2 file(s)');
    expect(describeFileErrors('Failed to import 2 file(s)', undefined, 2)).toBe('Failed to import 2 file(s)');
  });

  it('omits "and more" when every failure is named', () => {
    expect(describeFileErrors('1 file could not be indexed', [{ name: 'a.pdf', reason: 'empty' }], 1))
      .toBe('1 file could not be indexed: a.pdf: empty');
  });
});

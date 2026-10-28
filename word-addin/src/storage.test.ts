import { describe, it, expect, vi } from 'vitest';
import { createStore, readJson } from './storage';

describe('storage', () => {
  it('uses localStorage when OfficeRuntime is absent', async () => {
    const store = createStore();
    await store.set('k', 'v');
    expect(localStorage.getItem('k')).toBe('v');
    expect(await store.get('k')).toBe('v');
    await store.remove('k');
    expect(await store.get('k')).toBeNull();
  });

  it('prefers OfficeRuntime.storage when present', async () => {
    const backing = new Map<string, string>();
    (globalThis as Record<string, unknown>).OfficeRuntime = {
      storage: {
        getItem: vi.fn(async (k: string) => backing.get(k) ?? null),
        setItem: vi.fn(async (k: string, v: string) => {
          backing.set(k, v);
        }),
        removeItem: vi.fn(async (k: string) => {
          backing.delete(k);
        }),
      },
    };
    const store = createStore();
    await store.set('k', 'office');
    expect(backing.get('k')).toBe('office');
    expect(localStorage.getItem('k')).toBeNull();
    expect(await store.get('k')).toBe('office');
    await store.remove('k');
    expect(await store.get('k')).toBeNull();
  });

  it('degrades to "nothing saved" when the store throws', async () => {
    (globalThis as Record<string, unknown>).OfficeRuntime = {
      storage: {
        getItem: vi.fn(async () => {
          throw new Error('blocked');
        }),
        setItem: vi.fn(async () => {
          throw new Error('blocked');
        }),
        removeItem: vi.fn(async () => {
          throw new Error('blocked');
        }),
      },
    };
    const store = createStore();
    await expect(store.set('k', 'v')).resolves.toBeUndefined();
    await expect(store.get('k')).resolves.toBeNull();
    await expect(store.remove('k')).resolves.toBeUndefined();
  });

  it('readJson returns null for missing or malformed values', async () => {
    const store = createStore();
    expect(await readJson(store, 'none')).toBeNull();
    await store.set('bad', '{not json');
    expect(await readJson(store, 'bad')).toBeNull();
    await store.set('good', JSON.stringify({ a: 1 }));
    expect(await readJson<{ a: number }>(store, 'good')).toEqual({ a: 1 });
  });
});

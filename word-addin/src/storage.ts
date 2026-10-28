/**
 * Small async key/value store for the pane.
 *
 * `OfficeRuntime.storage` is the store Office gives add-ins; it persists per
 * add-in across documents and sessions and is not shared with web pages. In a
 * plain browser (development, tests) it does not exist, so localStorage stands
 * in. Every access is wrapped: a blocked store must degrade to "nothing saved",
 * never to a crash of the pane.
 */

export interface KeyValueStore {
  get(key: string): Promise<string | null>;
  set(key: string, value: string): Promise<void>;
  remove(key: string): Promise<void>;
}

type RuntimeStorage = {
  getItem(key: string): Promise<string | null>;
  setItem(key: string, value: string): Promise<void>;
  removeItem(key: string): Promise<void>;
};

function officeRuntimeStorage(): RuntimeStorage | null {
  const g = globalThis as { OfficeRuntime?: { storage?: RuntimeStorage } };
  const storage = g.OfficeRuntime?.storage;
  return storage && typeof storage.getItem === 'function' ? storage : null;
}

export function createStore(): KeyValueStore {
  return {
    async get(key) {
      const runtime = officeRuntimeStorage();
      try {
        if (runtime) return (await runtime.getItem(key)) ?? null;
        return localStorage.getItem(key);
      } catch {
        return null;
      }
    },
    async set(key, value) {
      const runtime = officeRuntimeStorage();
      try {
        if (runtime) await runtime.setItem(key, value);
        else localStorage.setItem(key, value);
      } catch {
        // A blocked store only means the sign-in is not remembered.
      }
    },
    async remove(key) {
      const runtime = officeRuntimeStorage();
      try {
        if (runtime) await runtime.removeItem(key);
        else localStorage.removeItem(key);
      } catch {}
    },
  };
}

export const SESSION_KEY = 'casecite.session.v1';
export const SERVER_URL_KEY = 'casecite.serverUrl.v1';
export const MATTER_KEY = 'casecite.matterId.v1';

export async function readJson<T>(store: KeyValueStore, key: string): Promise<T | null> {
  const raw = await store.get(key);
  if (!raw) return null;
  try {
    return JSON.parse(raw) as T;
  } catch {
    return null;
  }
}

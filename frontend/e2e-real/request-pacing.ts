import { setTimeout as delay } from 'node:timers/promises';
import type { APIRequestContext, BrowserContext, Request } from '@playwright/test';

// The browser and API fixture share one client IP. Pace both through the same
// queue: 250 ms between API calls leaves headroom below 300 requests/minute.
// Keep server limits and responses intact; a 429 must still fail the test.
export async function paceAPIRequests(context: BrowserContext, request: APIRequestContext, baseURL: string): Promise<APIRequestContext> {
  const origin = new URL(baseURL).origin;
  let nextStart = 0;
  let queue = Promise.resolve();
  const pace = () => {
    queue = queue.then(async () => {
      // Timers may wake early. Measure actual dispatch time, not reserved slots,
      // so an event-loop delay cannot release several requests in a burst.
      while (performance.now() < nextStart) await delay(nextStart - performance.now());
      nextStart = performance.now() + 250;
    });
    return queue;
  };
  const isAPI = (url: string) => {
    const parsed = new URL(url, baseURL);
    return parsed.origin === origin && parsed.pathname.startsWith('/api/');
  };
  await context.route(url => isAPI(url.href), async route => {
    await pace();
    await route.continue();
  });

  // Browser routing does not intercept the separate APIRequestContext. Bind
  // its methods to the original context to preserve cookies and normal errors.
  const methods = new Set(['get', 'post', 'put', 'patch', 'delete', 'head', 'fetch']);
  return new Proxy(request, {
    get(target, property) {
      const value = Reflect.get(target, property);
      if (typeof value !== 'function') return value;
      if (!methods.has(String(property))) return value.bind(target);
      return async (...args: unknown[]) => {
        const input = args[0] as string | Request;
        if (isAPI(typeof input === 'string' ? input : input.url())) await pace();
        return Reflect.apply(value, target, args);
      };
    },
  });
}

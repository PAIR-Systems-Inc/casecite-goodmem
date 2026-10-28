import { describe, it, expect, vi, beforeEach } from 'vitest';

vi.mock('./client', () => {
  const api = {
    request: vi.fn(),
    authFetch: vi.fn(),
  };
  return { api, API_BASE_URL: 'http://localhost:8000/api/v1' };
});

import { api } from './client';
import './deviceAuth';

describe('api/deviceAuth', () => {
  beforeEach(() => { vi.clearAllMocks(); });

  it('registers the device-code decision methods on the api object', () => {
    expect(typeof (api as any).approveDeviceCode).toBe('function');
    expect(typeof (api as any).denyDeviceCode).toBe('function');
  });

  it('approveDeviceCode posts the user code to /auth/device/approve', async () => {
    (api.request as any).mockResolvedValue({ status: 'approved', user_code: 'ABCD-EFGH' });
    const result = await (api as any).approveDeviceCode('ABCDEFGH');
    expect(api.request).toHaveBeenCalledWith('/auth/device/approve', {
      method: 'POST',
      body: JSON.stringify({ user_code: 'ABCDEFGH' }),
    });
    expect(result.status).toBe('approved');
  });

  it('denyDeviceCode posts the user code to /auth/device/deny', async () => {
    (api.request as any).mockResolvedValue({ status: 'denied', user_code: 'ABCD-EFGH' });
    const result = await (api as any).denyDeviceCode('ABCDEFGH');
    expect(api.request).toHaveBeenCalledWith('/auth/device/deny', {
      method: 'POST',
      body: JSON.stringify({ user_code: 'ABCDEFGH' }),
    });
    expect(result.status).toBe('denied');
  });
});

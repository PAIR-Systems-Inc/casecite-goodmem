import { api } from './client';
import type { DeviceDecision } from './types';

/**
 * Device-code sign-in: the signed-in web user approves or denies the short
 * code a cookie-less client (the Word add-in's task pane) is showing. Both
 * calls are cookie-authenticated and CSRF-protected like any other POST.
 */
Object.assign(api, {
  async approveDeviceCode(userCode: string): Promise<DeviceDecision> {
    return api.request('/auth/device/approve', {
      method: 'POST',
      body: JSON.stringify({ user_code: userCode }),
    });
  },

  async denyDeviceCode(userCode: string): Promise<DeviceDecision> {
    return api.request('/auth/device/deny', {
      method: 'POST',
      body: JSON.stringify({ user_code: userCode }),
    });
  },
});

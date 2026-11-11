import { api } from './client';

api.demoSession = () => api.request('/demo/session');
api.demoRedeem = (token) => api.request('/demo/redeem', { method: 'POST', body: JSON.stringify({ token }) });
api.demoLogout = () => api.request('/demo/logout', { method: 'POST' });
api.demoDocuments = (scope) => api.request(`/demo/${scope}/documents`);
api.demoChat = (scope, question) => api.request(`/demo/${scope}/chat`, { method: 'POST', body: JSON.stringify({ question }), timeout: 180000 });
api.demoUpload = (file) => {
  const body = new FormData();
  body.append('file', file);
  return api.request('/demo/private/documents', { method: 'POST', body, timeout: 180000 });
};

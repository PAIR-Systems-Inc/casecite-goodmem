import { api, API_BASE_URL } from './client';
import type {
  ReviewTableCreateInput,
  ReviewTableDetail,
  ReviewTableEstimate,
  ReviewTableJob,
  ReviewTableRunStarted,
  ReviewTableSummary,
  ReviewTableUpdateInput,
} from './types';

const tablePath = (tableId: string, suffix = '') => `/review-tables/${encodeURIComponent(tableId)}${suffix}`;

Object.assign(api, {
  async listReviewTables(): Promise<{ tables: ReviewTableSummary[] }> {
    return api.request('/review-tables');
  },

  async createReviewTable(input: ReviewTableCreateInput): Promise<ReviewTableDetail> {
    return api.request('/review-tables', {
      method: 'POST',
      body: JSON.stringify({
        name: input.name,
        description: input.description || null,
        matter_id: input.matter_id || null,
        fields: input.fields,
        document_ids: input.document_ids || [],
      }),
    });
  },

  async getReviewTable(tableId: string): Promise<ReviewTableDetail> {
    return api.request(tablePath(tableId));
  },

  async updateReviewTable(tableId: string, changes: ReviewTableUpdateInput): Promise<ReviewTableDetail> {
    return api.request(tablePath(tableId), { method: 'PATCH', body: JSON.stringify(changes) });
  },

  async deleteReviewTable(tableId: string): Promise<{ status: string; id: string }> {
    return api.request(tablePath(tableId), { method: 'DELETE' });
  },

  async estimateReviewTable(tableId: string): Promise<ReviewTableEstimate> {
    return api.request(tablePath(tableId, '/estimate'));
  },

  async runReviewTable(
    tableId: string,
    scope: { document_ids?: string[]; field_keys?: string[] } = {},
  ): Promise<ReviewTableRunStarted> {
    return api.request(tablePath(tableId, '/run'), { method: 'POST', body: JSON.stringify(scope) });
  },

  async getReviewTableJob(jobId: string): Promise<ReviewTableJob> {
    return api.request(`/jobs/${jobId}`);
  },

  /** The cited export as a file: an .xlsx workbook (Results + Citations) or a flat CSV. */
  async exportReviewTable(tableId: string, format: 'xlsx' | 'csv'): Promise<Blob> {
    const resp = await api.authFetch(`${API_BASE_URL}${tablePath(tableId, `/export?format=${format}`)}`);
    if (!resp.ok) throw new Error(`Export failed (${resp.status})`);
    return resp.blob();
  },
});

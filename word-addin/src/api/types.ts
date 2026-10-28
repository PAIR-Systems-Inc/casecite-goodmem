/** Shapes of the CaseCite API responses the add-in reads. Mirrors backend models. */

export interface UserInfo {
  id: string;
  email: string;
  name: string;
  roles: string[];
  must_change_password?: boolean;
  mfa_enrollment_required?: boolean;
}

/** POST /auth/device/start */
export interface DeviceStart {
  device_code: string;
  user_code: string;
  verification_uri: string;
  verification_uri_complete: string;
  expires_in: number;
  interval: number;
}

export type DevicePollStatus =
  | 'authorization_pending'
  | 'slow_down'
  | 'expired_token'
  | 'access_denied'
  | 'approved';

/** POST /auth/device/token */
export interface DeviceTokenResponse {
  status: DevicePollStatus;
  access_token?: string | null;
  refresh_token?: string | null;
  expires_in?: number | null;
  user?: UserInfo | null;
}

/** POST /auth/refresh (device session: the rotated refresh token is in the body) */
export interface TokenRefreshResponse {
  access_token: string;
  refresh_token?: string | null;
  expires_in: number;
}

/** GET /matters */
export interface Matter {
  id: string;
  name: string;
  client_name: string | null;
  is_personal: boolean;
  role: string;
}

/** GET /matters/{id}/documents */
export interface MatterDocument {
  id: string;
  name?: string;
  filename?: string;
}

export interface ContractKeyTerm {
  value: string;
  quote: string | null;
  span_start: number | null;
  span_end: number | null;
  verified: boolean;
}

export interface ContractIssue {
  ref: string;
  title: string;
  status: string;
  severity: string; // critical | major | minor | informational
  why: string;
  suggested_language?: string | null;
  matched_text?: string | null;
  grounding: 'span' | 'absence' | 'unverified';
}

/** POST /contract-analysis/analyze (synchronous form) */
export interface ContractAnalysisResult {
  analysis_id: string;
  contract_type: string;
  executive_summary?: string | null;
  key_terms: Record<string, ContractKeyTerm>;
  issues: ContractIssue[];
}

export interface Citation {
  id: string;
  source: string;
  type: string;
  passage: string;
  status: string;
  url?: string | null;
  document_id?: string | null;
}

/** POST /chat (research mode) */
export interface ChatResponse {
  id: string;
  content: string;
  citations: Citation[];
  session_id?: string | null;
}

/** What the pane keeps between openings. */
export interface StoredSession {
  serverUrl: string;
  accessToken: string;
  refreshToken: string;
  user: UserInfo;
}

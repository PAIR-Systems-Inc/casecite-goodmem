# Roadmap

What ships today, what is planned, and — just as important for a firm
evaluating the product — what does **not** exist yet.

## Current
- Research chat over the firm's documents with citations back to the source and a case-law guard
- Contract analysis, redlining as native Word tracked changes, and long-document drafting
- Strategy briefs with quote-verified authorities (API only)
- Judge intelligence, citation check, and case-law tools backed by CourtListener
- Matters: a workspace per client engagement (`/matters`) with the documents, chats, analyses, members and activity filed under it; an active-matter selector that scopes Research, Contracts and Drafting; bulk filing of documents between matters; ownership transfer for offboarding
- HMAC-chained audit log with startup and on-demand verification, viewable and exportable by admins
- BYOK provider keys and uploaded originals encrypted at rest (AES-256-GCM)
- TOTP MFA (optionally mandatory), password reset and change, session lifetime and idle timeout
- User administration: invite, roles and permissions, deactivate, delete, MFA reset
- OpenAI, Anthropic and Gemini for chat; OpenAI, Voyage, Cohere or a self-hosted server for embeddings

## Next
- Connector folder picker (the sync API takes a folder; the UI can only import a whole connected account)
- Azure AD SSO sign-in flow in the frontend (the backend token exchange is complete)
- Ownership transfer of documents when a user is offboarded (matters can be handed over today; deleting a user still deletes their documents, so transfer their matters and deactivate instead)
- Ethical walls: admin-managed matter access and deny-lists (today, access is by matter membership only)
- Retention policies and legal hold for documents and chats (today, only audit logs have retention)
- In-place `SECRET_KEY` rotation with re-encryption of stored documents
- Per-user usage metering and quotas for server-level provider keys
- A reranking stage for retrieval (today: vector search with keyword rescoring)
- Preserve run-level formatting from the source .docx in redline exports

## Future
- SAML / generic OIDC single sign-on and SCIM provisioning (today: Azure AD only)
- External anchoring of the audit-chain head (object-lock bucket or SIEM forwarding)
- Application-level encryption of the vector index and database text (today these rely on disk encryption)
- Multi-worker / multi-replica deployments (sessions and document registry in Redis/Postgres)
- Integration with additional court e-filing systems
- Multi-language document support

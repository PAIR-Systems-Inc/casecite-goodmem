# Google Workspace single sign-on

Google SSO uses the same local users, roles, sessions, MFA challenge and required
MFA enrollment as password and Microsoft sign-in. It adds no database, Redis,
identity proxy or running service. The existing Go OIDC library verifies Google's
signed tokens; Google's Identity Services script renders the sign-in button.

## Enable

1. Create a **Web application** OAuth client in Google Cloud and configure its
   consent screen for your organization. Register the exact CaseCite `PUBLIC_URL`
   origin under **Authorized JavaScript origins**. For local development, register
   both `http://localhost` and `http://localhost:<port>`. This popup ID-token flow does not need a redirect callback or
   an application-side client secret.
2. Set `GOOGLE_SSO_CLIENT_ID` and `GOOGLE_SSO_ALLOWED_DOMAINS` (comma-separated
   exact Workspace domains, for example `example.com,example.org`). Keep
   `GOOGLE_SSO_AUTO_PROVISION=false` to require an invitation. If explicitly
   enabled, `GOOGLE_SSO_DEFAULT_ROLE` defaults to `viewer`; it cannot grant admin.
3. Restart the application. **Sign in with Google** appears in the sign-in dialog.
   The names are separate from the Google Drive connector credentials and the
   Google/Gemini model API key.

An invited user can claim their unused, non-admin invitation using its verified
email address. Existing users first sign in normally, then visit **Settings →
Security** and link the Google account with the same verified email. Existing
accounts are never silently linked merely because an email matches. Google
sign-in does not remove an existing local password.

## Validation and boundaries

The server checks the token signature, issuer, audience, expiration, nonce,
verified email, and exact Google `hd` Workspace claim. An email address with the
right suffix is insufficient. Identity is Google's immutable `sub`, so changing
an email cannot silently create a new identity or transfer an existing account.
The browser-bound nonce is stored in PostgreSQL for ten minutes and consumed
transactionally once. Linking challenges are also bound to the authenticated
CaseCite account. Provider role claims never override local roles.

Disabled accounts remain disabled. SSO does not bypass CaseCite MFA or mandatory
MFA enrollment. The server does not infer MFA satisfaction from a Google login.
Consumer Gmail accounts are outside the Workspace allowlist configuration.

Signed-token backend tests cover wrong issuer/audience/nonce/domain, missing
domain, unverified email, expiry, replay, invitation-only enrollment, account
linking, local roles, disabled users and MFA. Frontend tests cover the button,
nonce, session/MFA handling, retries and late popup callbacks. A real organization
sign-in still requires your configured Google OAuth client and Workspace account.

References: [Google OpenID Connect](https://developers.google.com/identity/openid-connect/openid-connect)
and [Google Identity Services setup](https://developers.google.com/identity/gsi/web/guides/get-google-api-clientid).

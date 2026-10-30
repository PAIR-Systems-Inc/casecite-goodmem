// OAuth origins a connector auth_url may legitimately point at.
const ALLOWED_OAUTH_ORIGINS = new Set([
  'https://accounts.google.com',
  'https://login.microsoftonline.com',
  'https://app.box.com',
  'https://account.box.com',
  'https://www.dropbox.com',
  'https://vault.netvoyage.com',
  'https://cloudimanage.com',
  'https://app.clio.com',
  'https://eu.app.clio.com',
]);

/**
 * True when a backend-supplied OAuth URL points at a known provider. Compares
 * the parsed origin exactly — a prefix check would accept
 * `https://accounts.google.com.attacker.example/…`.
 */
export function isAllowedOAuthUrl(url: string | null | undefined): url is string {
  if (!url) return false;
  try {
    const target = new URL(url);
    if (target.username || target.password) return false;
    // Regional and on-premise destinations are resolved by the server using
    // the browser-bound OAuth state, never an arbitrary redirect parameter.
    if (target.origin === window.location.origin && /^\/api\/v1\/connectors\/(google_drive|onedrive|box|dropbox|filevine|clio|netdocuments|imanage)\/authorize$/.test(target.pathname)) {
      return !!target.searchParams.get('state');
    }
    return ALLOWED_OAUTH_ORIGINS.has(target.origin);
  } catch {
    return false;
  }
}

import { describe, it, expect } from 'vitest';
import { isAllowedOAuthUrl } from './oauthOrigins';

describe('isAllowedOAuthUrl', () => {
  it('accepts known provider origins', () => {
    expect(isAllowedOAuthUrl('https://accounts.google.com/o/oauth2/v2/auth?client_id=x')).toBe(true);
    expect(isAllowedOAuthUrl('https://account.box.com/api/oauth2/authorize')).toBe(true);
  });

  it('rejects a look-alike host that merely starts with a provider origin', () => {
    expect(isAllowedOAuthUrl('https://accounts.google.com.attacker.example/auth')).toBe(false);
    expect(isAllowedOAuthUrl('https://app.box.com@attacker.example/')).toBe(false);
  });

  it('rejects other schemes, unknown hosts and junk', () => {
    expect(isAllowedOAuthUrl('http://accounts.google.com/')).toBe(false);
    expect(isAllowedOAuthUrl('javascript:alert(1)')).toBe(false);
    expect(isAllowedOAuthUrl('https://example.com/')).toBe(false);
    expect(isAllowedOAuthUrl('not a url')).toBe(false);
    expect(isAllowedOAuthUrl(undefined)).toBe(false);
    expect(isAllowedOAuthUrl(null)).toBe(false);
  });

  it('accepts only a same-origin connector launch with a state', () => {
    const origin = window.location.origin;
    expect(isAllowedOAuthUrl(`${origin}/api/v1/connectors/imanage/authorize?state=bound`)).toBe(true);
    expect(isAllowedOAuthUrl(`${origin}/api/v1/connectors/filevine/authorize?state=bound`)).toBe(true);
    expect(isAllowedOAuthUrl(`${origin}/api/v1/connectors/imanage/authorize`)).toBe(false);
    expect(isAllowedOAuthUrl(`${origin}/redirect?state=bound&url=https://attacker.example`)).toBe(false);
    expect(isAllowedOAuthUrl('https://attacker.example/api/v1/connectors/imanage/authorize?state=bound')).toBe(false);
  });
});

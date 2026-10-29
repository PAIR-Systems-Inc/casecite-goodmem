/**
 * Device-code helpers shared by the /device approval page.
 *
 * The server hands the Word add-in an eight-character code from an alphabet
 * without look-alike characters and shows it as `XXXX-XXXX`; a person may type
 * it back in any case, with or without the hyphen or spaces.
 */

export const DEVICE_USER_CODE_LENGTH = 8;

/** `abcd-efgh` / `ABCD EFGH` / `abcdefgh` -> `ABCDEFGH` (only letters and digits kept). */
export function normalizeDeviceUserCode(raw: string): string {
  return raw.toUpperCase().replace(/[^A-Z0-9]/g, '').slice(0, DEVICE_USER_CODE_LENGTH);
}

/** `ABCDEFGH` -> `ABCD-EFGH`; shorter input is shown as typed so far. */
export function formatDeviceUserCode(raw: string): string {
  const code = normalizeDeviceUserCode(raw);
  return code.length > 4 ? `${code.slice(0, 4)}-${code.slice(4)}` : code;
}

export function isCompleteDeviceUserCode(raw: string): boolean {
  return normalizeDeviceUserCode(raw).length === DEVICE_USER_CODE_LENGTH;
}

import { describe, it, expect } from 'vitest';
import {
  formatDeviceUserCode,
  isCompleteDeviceUserCode,
  normalizeDeviceUserCode,
} from './deviceCode';

describe('deviceCode helpers', () => {
  it('normalises case, separators and length', () => {
    expect(normalizeDeviceUserCode('abcd-efgh')).toBe('ABCDEFGH');
    expect(normalizeDeviceUserCode(' ab cd ef gh ')).toBe('ABCDEFGH');
    expect(normalizeDeviceUserCode('ABCDEFGHIJK')).toBe('ABCDEFGH');
    expect(normalizeDeviceUserCode('')).toBe('');
  });

  it('formats as XXXX-XXXX once more than four characters are typed', () => {
    expect(formatDeviceUserCode('ab')).toBe('AB');
    expect(formatDeviceUserCode('abcd')).toBe('ABCD');
    expect(formatDeviceUserCode('abcde')).toBe('ABCD-E');
    expect(formatDeviceUserCode('ABCD-EFGH')).toBe('ABCD-EFGH');
  });

  it('knows when a code is complete', () => {
    expect(isCompleteDeviceUserCode('ABCD-EFG')).toBe(false);
    expect(isCompleteDeviceUserCode('abcd efgh')).toBe(true);
  });
});

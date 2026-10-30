import { describe, expect, it } from 'vitest';
import { treatmentLabel } from './DeepResearchSources';

describe('research treatment evidence', () => {
  it('keeps an unavailable treatment scan undetermined', () => {
    expect(treatmentLabel({ status: 'checked', warning_level: 'unknown', is_good_law: null, negative: 0, caution: 0 }))
      .toEqual({ text: 'Treatment undetermined', tone: 'muted' });
  });
  it('distinguishes a successful scan from identified treatment language', () => {
    expect(treatmentLabel({ status: 'checked', warning_level: 'none', is_good_law: true })?.tone).toBe('ok');
    expect(treatmentLabel({ status: 'checked', warning_level: 'warning', is_good_law: null, negative: 1, caution: 0 })?.text)
      .toContain('1 citing opinion');
    expect(treatmentLabel({ status: 'error' })?.tone).toBe('muted');
  });
});

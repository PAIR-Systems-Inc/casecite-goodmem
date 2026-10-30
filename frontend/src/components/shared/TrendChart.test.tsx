import { render, screen } from '@testing-library/react';
import { describe, it, expect } from 'vitest';
import { TrendChart } from './TrendChart';

describe('TrendChart', () => {
  it('identifies the current year when a future year follows it', () => {
    const current = new Date().getFullYear();
    render(<TrendChart styles={{}} topic="notice" currentYearPartial trend="increasing" years={[
      { year: current - 2, count: 100 }, { year: current - 1, count: 120 },
      { year: current, count: 130 }, { year: current + 1, count: 0 },
    ]} />);
    expect(screen.getByText(`${current} is still in progress; it is shown but not counted toward the trend.`)).toBeInTheDocument();
    expect(screen.getByText('Peak · 120 cases')).toBeInTheDocument();
    expect(screen.getByText(`${current} · 130 cases so far`)).toBeInTheDocument();
  });

  it('reports unavailable years without inventing a rate-limit diagnosis', () => {
    render(<TrendChart styles={{}} topic="notice" years={[]} yearsFailed={5} />);
    expect(screen.getByText(/yearly counts could not be retrieved/)).toBeInTheDocument();
    expect(screen.queryByText(/rate-limiting/)).not.toBeInTheDocument();
  });
});

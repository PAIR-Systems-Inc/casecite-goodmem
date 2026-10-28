import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { useCaseViewStore } from '../stores/caseViewStore';
import { api } from '../api';
import type { CaseInfo } from '../types';
import CaseView from './CaseView';

// CaseView reads its state from the case view store, and its route for the
// case id (and how to resolve it: ?kind=cluster|opinion).
function renderAt(path = '/case') {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/case/:id" element={<CaseView />} />
        <Route path="/case" element={<CaseView />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe('CaseView', () => {
  beforeEach(() => {
    useCaseViewStore.setState({
      mainViewCase: null,
      caseLoading: false,
      caseMessages: [],
      caseQueryInput: '',
      caseQueryLoading: false,
      showCaseDocSelector: false,
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('renders without crashing', () => {
    const { container } = renderAt();
    expect(container).toBeTruthy();
  });

  it('shows back button', () => {
    renderAt();
    expect(screen.getByLabelText(/back/i)).toBeInTheDocument();
  });

  it('shows loading state', () => {
    useCaseViewStore.setState({ caseLoading: true });
    renderAt();
    expect(screen.getByText(/Loading case details/)).toBeInTheDocument();
  });

  it('renders case data when provided', () => {
    useCaseViewStore.setState({
      mainViewCase: {
        id: 'case-1',
        name: 'Smith v. Jones',
        case_name: 'Smith v. Jones',
        citation: '123 F.3d 456',
        court: 'ca9',
      } as unknown as CaseInfo,
    });
    renderAt();
    expect(screen.getByText(/Smith v. Jones/)).toBeInTheDocument();
  });

  it('passes the route id kind through to the case lookup', async () => {
    const authFetch = vi.spyOn(api, 'authFetch').mockResolvedValue(
      new Response(JSON.stringify({ id: 42, case_name: 'Roe v. Wade' }), { status: 200 }),
    );
    renderAt('/case/42?kind=opinion');
    expect(await screen.findByText(/Roe v. Wade/)).toBeInTheDocument();
    expect(authFetch).toHaveBeenCalledWith(expect.stringMatching(/\/tools\/cases\/42\?kind=opinion$/));
  });

  it('loadCaseDetail sends the kind it was given and omits it otherwise', async () => {
    const authFetch = vi.spyOn(api, 'authFetch').mockImplementation(async () =>
      new Response(JSON.stringify({ id: 7 }), { status: 200 }));
    await useCaseViewStore.getState().loadCaseDetail('7', 'cluster');
    expect(authFetch).toHaveBeenLastCalledWith(expect.stringMatching(/\/tools\/cases\/7\?kind=cluster$/));

    await useCaseViewStore.getState().loadCaseDetail('8');
    expect(authFetch).toHaveBeenLastCalledWith(expect.stringMatching(/\/tools\/cases\/8$/));
  });
});

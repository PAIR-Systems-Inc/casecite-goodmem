// office.js is never loaded in tests. Each test that needs the Office, Word or
// OfficeRuntime globals installs its own stub on globalThis and removes it
// afterwards; this file only makes sure no stub leaks between tests.
afterEach(() => {
  const g = globalThis as Record<string, unknown>;
  delete g.Office;
  delete g.Word;
  delete g.OfficeRuntime;
  localStorage.clear();
  sessionStorage.clear();
});

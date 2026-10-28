/**
 * Route configuration: maps URL paths to mode IDs and vice versa.
 */
const ROUTE_TO_MODE: Record<string, string> = {
  '/research': 'research',
  '/documents': 'rag-docs',
  '/judge-intel': 'judge-intel',
  '/case-citations': 'case-citations',
  '/contracts': 'contracts',
  '/drafting': 'drafting',
  '/case': 'case',
  '/matters': 'matters',
  '/review-tables': 'review-tables',
};

export const MODE_TO_ROUTE: Record<string, string> = {
  'research': '/research',
  'rag-docs': '/documents',
  'judge-intel': '/judge-intel',
  'case-citations': '/case-citations',
  'contracts': '/contracts',
  'drafting': '/drafting',
  'case': '/case',
  'matters': '/matters',
  'review-tables': '/review-tables',
};

/**
 * Derive the activeMode string from a pathname.
 */
export function modeFromPath(pathname: string): string {
  // /case and /case/:id — but not /case-citations, which has its own entry.
  if (pathname === '/case' || pathname.startsWith('/case/')) return 'case';
  // /matters and /matters/:id share the Matters mode.
  if (pathname === '/matters' || pathname.startsWith('/matters/')) return 'matters';
  // /review-tables and /review-tables/:id share the Tabular Review mode.
  if (pathname === '/review-tables' || pathname.startsWith('/review-tables/')) return 'review-tables';
  return ROUTE_TO_MODE[pathname] || 'research';
}

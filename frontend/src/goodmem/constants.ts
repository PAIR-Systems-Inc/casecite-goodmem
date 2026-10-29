export type View = 'research' | 'documents' | 'analyze' | 'draft' | 'compare' | 'review' | 'history' | 'settings';
export const labels: Record<View, string> = {
  research: 'Research',
  documents: 'Document library',
  analyze: 'Contract review',
  draft: 'Drafting',
  compare: 'Compare documents',
  review: 'Review tables',
  history: 'Saved work',
  settings: 'Settings',
};
export const descriptions: Partial<Record<View, string>> = {
  research: 'Build an argument you can trace back to its sources.',
  analyze: 'Understand the terms, obligations, and risks in your contracts.',
  draft: 'Turn instructions and reference documents into a working draft.',
  compare: 'See material changes and their practical effects.',
  review: 'Extract consistent answers across your document set.',
};
export const prompts: Partial<Record<View, string>> = {
  research: 'What does the agreement say about termination and notice?',
  analyze: 'Review from the customer’s perspective. Focus on liability, termination, data protection, and payment.',
  draft:
    'Draft a mutual NDA for two companies evaluating a commercial partnership. Use placeholders for missing names and dates.',
  compare: 'Compare liability limits, notice periods, and renewal terms.',
  review: 'Extract only terms supported by the document. Flag ambiguous provisions.',
};

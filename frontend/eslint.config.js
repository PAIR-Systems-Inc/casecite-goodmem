// ESLint 9 flat config. Replaces .eslintrc.cjs (ESLint 8, end-of-life).
import js from '@eslint/js';
import tseslint from 'typescript-eslint';
import react from 'eslint-plugin-react';
import reactHooks from 'eslint-plugin-react-hooks';
import prettier from 'eslint-config-prettier/flat';
import globals from 'globals';

export default tseslint.config(
  {
    // Build output, dependencies and tooling reports are never linted.
    ignores: ['dist/**', 'node_modules/**', 'coverage/**', 'playwright-report/**', 'test-results/**'],
  },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  react.configs.flat.recommended,
  react.configs.flat['jsx-runtime'],
  reactHooks.configs.flat.recommended,
  {
    files: ['**/*.{js,jsx,ts,tsx}'],
    languageOptions: {
      ecmaVersion: 'latest',
      sourceType: 'module',
      globals: { ...globals.browser, ...globals.es2020 },
    },
    settings: { react: { version: '18.2' } },
    rules: {
      'no-console': ['warn', { allow: ['warn', 'error'] }],
      'react/prop-types': 'off',
      // `catch {}` for deliberate best-effort operations is fine.
      'no-empty': ['error', { allowEmptyCatch: true }],
      // Apostrophes/quotes in JSX copy are fine; escaping them hurts readability.
      'react/no-unescaped-entities': 'off',
      // eslint-plugin-react-hooks 7 ships the React Compiler rules. This one
      // flags the ~25 places that derive UI state from a store or prop change
      // inside an effect (open the chat dock when messages arrive, reset a form
      // when its modal opens, the reset-password stage machine). They are
      // deliberate and work; rewriting them as render-time derivations or
      // key-based resets is tracked separately and should land with tests, not
      // inside a dependency bump. `warn` would still fail CI (--max-warnings 0).
      'react-hooks/set-state-in-effect': 'off',
      // Underscore prefix marks intentionally-unused (e.g. dev-gated logger args).
      '@typescript-eslint/no-unused-vars': [
        'error',
        { argsIgnorePattern: '^_', varsIgnorePattern: '^_', caughtErrorsIgnorePattern: '^_' },
      ],
    },
  },
  {
    files: ['**/*.test.{js,jsx,ts,tsx}', 'src/test/**', 'e2e/**'],
    languageOptions: { globals: { ...globals.vitest, ...globals.node } },
    rules: {
      // Tests routinely stub/cast with `any`; strictness stays on for src.
      '@typescript-eslint/no-explicit-any': 'off',
    },
  },
  // Must be last: turns off every stylistic rule Prettier owns.
  prettier,
);

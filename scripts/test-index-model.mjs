// Isolated integration stack only. Read its ephemeral connection on stdin;
// neither the GoodMem key nor the provider credential is printed or persisted.
import { readFileSync } from 'node:fs';

const target = new URL(process.argv[2]);
if (!['localhost', '127.0.0.1'].includes(target.hostname)) throw new Error('Expected a loopback test GoodMem server');
const config = JSON.parse(readFileSync(0, 'utf8'));
const response = await fetch(new URL('/v1/embedders', target), {
  method: 'POST',
  headers: { 'Content-Type': 'application/json', 'x-api-key': config.api_key },
  body: JSON.stringify({
    displayName: 'Local alternate test embedding', providerType: 'OPENAI',
    endpointUrl: 'http://provider:8080/v1', modelIdentifier: 'test-embedding-alternate',
    dimensionality: 16, distributionType: 'DENSE', supportedModalities: ['TEXT'],
    credentials: { kind: 'CREDENTIAL_KIND_API_KEY', apiKey: { inlineSecret: 'test-provider-only' } },
    labels: { casecite: 'enabled' },
  }),
});
if (!response.ok) throw new Error(`Test embedder setup returned HTTP ${response.status}`);
console.log('Enabled alternate integration embedder.');

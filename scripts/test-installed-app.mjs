// API smoke test for a newly installed image; fixture data only, no browser or
// external model calls. Verify persisted account/documents again after restart.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
const [base, state, phase] = process.argv.slice(2);
const account = { email: 'installer@example.test', name: 'Installer Test', password: 'Cedar!Trail7826' };
const csrfResponse = await fetch(`${base}/api/v1/csrf-token`);
assert.equal(csrfResponse.status, 200);
const csrf = await csrfResponse.json();
const cookie = csrfResponse.headers.getSetCookie().map(c => c.split(';')[0]).join('; ');
const env = await fs.readFile(`${state}/.env`, 'utf8');
const bootstrap = env.match(/^REGISTRATION_BOOTSTRAP_TOKEN="([a-f0-9]+)"$/m)?.[1];
assert.ok(bootstrap);
const login = await fetch(`${base}/api/v1/auth/${phase === 'create' ? 'register' : 'login'}`, {
  method: 'POST',
  headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf.csrf_token, Cookie: cookie },
  body: JSON.stringify({ ...account, bootstrap_token: bootstrap }),
});
assert.equal(login.status, 200, `Account ${phase} failed: ${await login.clone().text()}`);
const { access_token } = await login.json();
assert.ok(access_token);
const headers = { Authorization: `Bearer ${access_token}` };
let documents;
if (phase === 'create') {
  documents = [];
  for (const fixture of [
    { name: 'installer.txt', type: 'text/plain', data: Buffer.from('Installer verification: notice is thirty days.') },
    { name: 'installer-scan.pdf', type: 'application/pdf', data: await fs.readFile('server/internal/app/testdata/scanned_notice.pdf') },
  ]) {
    const form = new FormData();
    form.append('file', new Blob([fixture.data], { type: fixture.type }), fixture.name);
    const response = await fetch(`${base}/api/v1/documents`, { method: 'POST', headers, body: form });
    assert.equal(response.status, 200, await response.clone().text());
    const doc = await response.json();
    documents.push({ id: doc.id, ...fixture, data: fixture.data.toString('base64') });
  }
  await fs.writeFile(`${state}/documents.json`, JSON.stringify(documents), { mode: 0o600 });
} else {
  documents = JSON.parse(await fs.readFile(`${state}/documents.json`, 'utf8'));
}
for (const doc of documents) {
  let indexed = false;
  for (let attempt = 0; attempt < 90; attempt++) {
    const response = await fetch(`${base}/api/v1/documents/${doc.id}`, { headers });
    assert.equal(response.status, 200);
    const current = await response.json();
    assert.notEqual(current.status, 'failed', 'Fixture indexing failed');
    if (current.status === 'indexed') { indexed = true; break; }
    await new Promise(resolve => setTimeout(resolve, 1000));
  }
  assert.ok(indexed, 'Document did not finish indexing');
  const original = await fetch(`${base}/api/v1/documents/${doc.id}/file`, { headers });
  assert.equal(original.status, 200);
  assert.deepEqual(Buffer.from(await original.arrayBuffer()), Buffer.from(doc.data, 'base64'));
  const content = await fetch(`${base}/api/v1/documents/${doc.id}/content`, { headers });
  assert.equal(content.status, 200);
  assert.match((await content.json()).text, /notice is (thirty|30) days/i);
  if (doc.type === 'application/pdf') {
    const page = await fetch(`${base}/api/v1/documents/${doc.id}/pages/0/image`, { headers });
    assert.equal(page.status, 200);
    assert.match(page.headers.get('content-type'), /^image\//);
  }
}
console.log(`Installed app ${phase}: account, native document, GoodMem OCR, indexing, original downloads and page images passed.`);

# Model providers and instance settings

CaseCite's Go server supports OpenAI-compatible generation, native Claude
Messages, and native Gemini `generateContent`. Personal encrypted keys take
precedence over `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, or `GOOGLE_API_KEY`
(`GEMINI_API_KEY` is an alias). Select the model in personal settings. A model
name containing `claude` selects Anthropic; `gemini` selects Google. Other names
use the configured OpenAI-compatible endpoint.

Routing, extraction, citation and other utility calls retain a selected Claude
or Gemini model. For OpenAI-compatible models they use the configured utility
model. Explicit drafting token budgets override the personal output limit;
otherwise the saved `max_tokens` setting applies. Model metadata in research
results and review cells records the model actually requested.

Without a native family key, Claude/Gemini names may be served by a deliberately
configured compatible gateway. They are never sent to OpenAI's native endpoint.
As upstream, a native key takes precedence over a compatible gateway. Provider
policies still apply to the actual destination. Local compatible servers need
no generation key; authenticated gateways use the OpenAI-compatible key.

## On-premise configuration

For a local deployment, set the actual model names served by your endpoints:

```dotenv
OPENAI_BASE_URL=http://generation:8000/v1
LLM_MODEL=your-local-chat-model
OPENAI_UTILITY_MODEL=your-local-utility-model
OPENAI_API_KEY=
EMBEDDING_BASE_URL=http://embeddings:8000/v1
EMBEDDING_MODEL=your-local-embedding-model
EMBEDDING_API_KEY=
HIPAA_ENFORCEMENT_ENABLED=true
APPROVED_AI_PROVIDERS=self_hosted
```

GoodMem's document embedder/reranker and [OCR runtime](ocr.md) are configured
separately. The Go allowlist governs Go generation and clause-embedding calls;
it does not inspect or override providers running inside GoodMem. Keep those
GoodMem providers on the intended infrastructure as well.

The optional allowlist accepts a JSON string array (as upstream) or a
comma-separated list. With enforcement enabled, an empty list blocks all Go AI
calls. `openai`, `anthropic`, `google`, `voyage`, and `cohere` identify native
providers. A public gateway uses its exact hostname, not the model name.
Loopback/private/link-local addresses, single-label internal hostnames,
`localhost`, and `host.docker.internal` use `self_hosted`; `local`, `self-hosted`
and `selfhosted` are aliases. A public DNS name for an internal deployment must
be explicitly listed. Network/DNS configuration remains the operator's concern.

## Administration

The original Settings → API Keys controls now use these Go routes:

| Routes | Behavior |
| --- | --- |
| `GET/POST/DELETE /api/v1/admin/integrations/local-llm` | Read, save and clear the instance endpoint/chat/utility-model override. A missing utility model uses the chat model. |
| `GET/POST/DELETE /api/v1/admin/integrations/courtlistener` | Read masked status, save and clear the shared CourtListener token. |

Both require `admin.settings`. Values are encrypted in PostgreSQL. Every Go
instance reads the same persisted configuration, so changes need no restart.
Clearing an override restores deployment environment values, including a local
endpoint, rather than unconditionally switching to a hosted provider. Personal
model choices continue to override the instance chat-model default. CourtListener
status distinguishes instance, environment and anonymous configuration. Audit
events omit tokens and prompts.

The UI serializes endpoint writes and resets. A pending or slower save cannot
silently restore an override after the user resets it. The reset label states
that it restores deployment defaults.

## Embeddings and failure handling

GoodMem handles document ingestion and retrieval embeddings. The separate
clause-classification comparison supports OpenAI, Voyage and Cohere, plus an
OpenAI-compatible `EMBEDDING_BASE_URL`. It never reuses the chat URL. A custom
embedding endpoint uses only `EMBEDDING_API_KEY`, never a personal cloud key.
If only a Voyage or Cohere key is available, their upstream default model is
selected. An explicitly selected model without its required key produces an
error instead of silently substituting a model from another provider.
Canonical clause-vector caches include endpoint, model and a credential digest.
Missing, duplicated, null or inconsistent vectors are rejected.

The shared HTTP layer limits generation responses to 4 MiB and embedding
responses to 32 MiB, refuses redirects, and has a 120-second total call deadline.
Transient connection/408/429/5xx failures have two retries, with cancellable
backoff and `Retry-After` capped at ten seconds. Unsupported OpenAI-compatible
parameters may be negotiated at most three times while retaining an output
budget. Long drafting uses this same retry budget, without a second retry loop.
Refusals, truncated output, interrupted bodies and malformed responses are
reported without publishing partial analysis. Provider error bodies are not
returned to the browser.

Claude uses API version `2026-10-21`, which removes sampling parameters. Gemini
requests use system instructions and JSON response MIME type when required.
Protocol references: [Claude Messages](https://platform.claude.com/docs/en/api/messages/create),
[Claude versioning](https://platform.claude.com/docs/en/api/versioning),
[Gemini generation](https://ai.google.dev/api/generate-content),
[OpenAI chat](https://developers.openai.com/api/reference/resources/chat),
[Voyage embeddings](https://docs.voyageai.com/reference/embeddings-api), and
[Cohere embeddings](https://docs.cohere.com/v1/reference/embed).

Verification includes 127 outputs generated from pinned Python routing/policy
helpers; PostgreSQL, protocol and race tests; and original-UI tests. Fixtures
verify protocols and state transitions, not live provider interoperability or
model answer quality. Those require evaluation with the intended deployment.

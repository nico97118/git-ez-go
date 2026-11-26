# Configuration

All settings use native `git config ez.KEY VALUE`, with Git's normal scopes,
includes, booleans and precedence. There is no dotenv/config-file format or custom
system-prompt setting. Initial integration requires a model; offline audit/help
and ordinary native continuation do not require provider credentials.

| Key (`ez.` prefix) | Default | Purpose |
| --- | --- | --- |
| provider | openai | `openai` compatible Chat Completions or native `ollama` |
| model | required | Exact provider model identifier |
| endpoint | provider default | OpenAI: `https://api.openai.com/v1/chat/completions`; Ollama: `http://localhost:11434/api/chat` |
| apiKey | unset | Explicit credential, takes precedence over selected environment token |
| apiKeyEnv | provider default | `OPENAI_API_KEY` or Ollama `LOCAL_MODEL_API_KEY`; anonymous local servers allowed |
| allowInsecureHttp | false | Explicit permission for a trusted non-loopback HTTP endpoint; loopback HTTP is supported |
| caBundle | unset | Native Git-expanded path to a regular CA bundle |
| requestOptions | `{}` | JSON object of native provider/model options; explicit options retain reference precedence |
| timeout | 120 | Command/request time control in seconds, 0.1–3600 |
| minConfidence | 0.8 | Acceptance policy, 0–1; confidence is not a correctness guarantee |
| maxFileBytes | 131072 | Eligible automatic text-file policy, 1–10000000 |
| validationAttempts | 3 | Independent check/repair policy, 1–1000 |
| maxRounds | 100 | Integration conflict-stop budget, 1–10000 |
| modelContextTokens | 32768 | Declared model context capacity, 4096–2000000 |
| modelMaxOutputTokens | 8192 | Output reservation/default generation capacity, 1–128000 and below context capacity |
| agentMaxTurns | 40 | Actual model requests, including nested summarization, 1–1000 |
| agentMaxToolCalls | 100 | Tool execution policy, including grouped calls, 1–10000 |
| toolOutputBytes | 32768 | Preview/offloading window, 1024–1000000; complete retained artifacts are independent |

Use an explicit `ez.apiKeyEnv` for private credentials rather than storing tokens
in repository configuration. Only the selected environment source is used; there
is no implicit SDK credential fallback. `ez.apiKey` overrides it when configured.
Do not commit either real credentials or audit traces containing private source.

OpenAI-compatible endpoints accept base/full Chat Completions URLs, not Responses.
Native Ollama accepts its base or `/api/chat` URL. Ollama options include native
sampling/generation controls and `reasoning`/`think`; options nested in `options`
retain reference normalization. The configuration adapter rejects options that
would replace model, messages, streaming mode, tools or output-format policy.
Unsupported Ollama SDK configuration names are rejected explicitly. Model-specific
capabilities remain the endpoint/model's responsibility; `probe` tests native tools.

Genkit configures its native input threshold as context capacity minus output
reservation. Configure actual Ollama `num_ctx` consistently with the declaration;
explicit native generation options otherwise retain precedence. No custom token
estimation, compression or byte admission policy is used.

`ez.agentMaxContextBytes` is retired and ignored, including malformed old values;
see [migration](migration.md). Other unknown settings are errors. Large diffs,
validation output and aggregate audit evidence do not inherit context-byte limits.
Independent safety bounds remain: native filesystem reads/slices, materialized
16 MiB audit manifests, 10000-entry audit discovery with `--path` fallback, bounded
metadata strings/recursion, OS storage limits and the existing sequence/file-policy
capture coupling. Model JSON and directory metadata still materialize in memory.
No OS sandbox or disk quota is claimed. Optional validation diagnostic loss is
explicitly warned and does not invalidate a successful check; mandatory proposal
and decision evidence must still be retained before staging.

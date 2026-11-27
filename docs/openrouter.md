# OpenRouter assessment

The pinned dependency is `github.com/firebase/genkit/go v1.14.0`. The engine owns
Git orchestration and proposal admission; `internal/agent/provider.go` registers a
native `compat_oai.NewChatModel` with tools, multi-turn history and system messages.
Genkit owns tool execution and context compression. The configured endpoint,
verbatim model ID, selected credential and native `requestOptions` already allow
OpenRouter without engine or harness changes. Base and full Chat Completions URLs
are supported. Responses endpoints are intentionally unsupported.

The official `plugins/compat_oai/openrouter` plugin is already included in that
Genkit module (Apache-2.0). Its source delegates model creation/conversion to
`compat_oai.ResolveChatAction` / `NewChatModel`, dynamically resolves arbitrary model
IDs and adds typed routing, reasoning and sampling options, environment defaults
and optional application-attribution headers. Its `MaxOutputTokens` maps to
`max_tokens`; the generic git-ez adapter defaults to `max_completion_tokens`, so the
README explicitly overrides the latter with null and supplies `max_tokens` through
Genkit's native `RequestConfig.Extra`. Routing fields pass through the same mechanism.
No application-attribution headers are required for protocol use.

The official plugin is compatible with the native tool protocol used here but
provides no separate tool harness or fix for opaque reasoning-history replay.
The shared v1.14.0 converter reads textual `reasoning`/`reasoning_content` and emits
`reasoning_content`; it does not preserve OpenRouter `reasoning_details`. Models
requiring that field across tool turns remain unqualified. Do not add a custom
history adapter to claim general catalog coverage. Probe each intended model, then
perform the separately authorized real-conflict smoke test in the README.

Decision: retain `ez.provider=openai`, explicitly select `OPENROUTER_API_KEY`, and
use native wire options. A second provider branch would duplicate configuration
and transport policy without addressing the shared converter limitation. No
Genkit upgrade, dependency, license/NOTICE change, CGO component, platform-specific
code, CI change or secret setup is needed. The existing pure-Go distribution and
third-party terms remain unchanged; this change makes no new native-platform claim.

Automated acceptance uses a loopback scripted server and a real temporary Git
conflict: load settings through Git, preserve a vendor-prefixed model ID, select
the OpenRouter environment credential over ambient OpenAI credentials, forward
routing/token options and retain tool-result IDs through proposal application.
This is protocol and engine integration evidence, not a live OpenRouter or model
quality qualification. Live tests are opt-in and were not run for this change.

Sources:

- [Genkit OpenRouter plugin](https://genkit.dev/docs/go/integrations/openrouter/)
- [Pinned plugin source](https://pkg.go.dev/github.com/firebase/genkit/go@v1.14.0/plugins/compat_oai/openrouter)
- [OpenRouter API](https://openrouter.ai/docs/api/reference/overview)
- [OpenRouter tools](https://openrouter.ai/docs/guides/features/tool-calling)

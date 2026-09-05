# LingoMux Go Library Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** Build an importable Go library that translates one text segment through Google, Microsoft, DeepL, or OpenAI, with explicit provider selection or ordered serial fallback.

**Architecture:** The root lingomux package owns stable contracts, validation, routing, deadlines, normalized errors, and privacy-safe hooks. Provider subpackages implement the public Provider interface and call vendor REST APIs with net/http, so the root never imports them and third parties can add adapters without changing the library.

**Tech Stack:** Go 1.26+, Go standard library, net/http, net/http/httptest, race detector.

**Spec:** docs/superpowers/specs/2026-09-05-lingomux-design.md

## Preconditions and fixed constraints

- [ ] Install Go 1.26 or newer before implementation. At plan-writing time, the current host returns go: command not found.
- [ ] Keep the module path exactly github.com/Sakuragi27/lingomux and license the project under MIT.
- [ ] Translate exactly one text segment per call; do not add batch, HTTP service, IM lifecycle, persistence, cache, queues, or parallel racing.
- [ ] Keep the default total timeout at 15 seconds and automatic order at Google, Microsoft, DeepL, OpenAI by registering providers in that order.
- [ ] Make all tests hermetic: fake providers and httptest.Server only, with no live vendor requests or real credentials.
- [ ] Never log or expose source text, translated text, credentials, or raw provider bodies through errors or hooks.

## Planned file tree

~~~text
go.mod
LICENSE
.gitignore
doc.go
request.go
request_test.go
result.go
provider.go
language.go
language_test.go
errors.go
errors_test.go
options.go
hook.go
client.go
client_test.go
lingomux_external_test.go
internal/httpjson/httpjson.go
internal/httpjson/httpjson_test.go
providers/google/google.go
providers/google/languages.go
providers/google/google_test.go
providers/microsoft/microsoft.go
providers/microsoft/languages.go
providers/microsoft/microsoft_test.go
providers/deepl/deepl.go
providers/deepl/languages.go
providers/deepl/deepl_test.go
providers/openai/openai.go
providers/openai/languages.go
providers/openai/openai_test.go
examples/basic/main.go
README.md
~~~

## Task 1: Bootstrap the module and legal metadata

**Files:**
- Create: go.mod
- Create: LICENSE
- Create: .gitignore
- Create: doc.go

- [ ] Write go.mod with module github.com/Sakuragi27/lingomux and go 1.26.0.
- [ ] Add the full MIT license text with copyright 2026 Sakuragi27.
- [ ] Ignore common Go build, coverage, editor, and OS artifacts without ignoring source or docs.
- [ ] Add a concise package comment in doc.go describing LingoMux as an embeddable translation router, not a service.
- [ ] Run gofmt on doc.go and run go test ./.... Expect a successful empty-package baseline.
- [ ] Commit:

~~~bash
git add go.mod LICENSE .gitignore doc.go
git commit -m "chore: bootstrap Go module"
~~~

## Task 2: Define contracts, normalization, validation, and errors

**Files:**
- Create: request.go
- Create: request_test.go
- Create: result.go
- Create: provider.go
- Create: language.go
- Create: language_test.go
- Create: errors.go
- Create: errors_test.go

- [ ] First write table-driven language tests for canonicalization:
  - en and JA become en and ja.
  - zh-cn, zh-CN, and ZH-cn become zh-CN.
  - zh-tw becomes zh-TW and pt-br becomes pt-BR.
  - auto is accepted only where source detection is allowed.
  - malformed tags such as en_US, a, zh--CN, auto-US, and whitespace are rejected.
- [ ] Write request validation tests:
  - whitespace-only text is invalid without mutating the original text.
  - target is required and target auto is invalid.
  - empty source and provider default to auto.
  - provider names normalize to lowercase ASCII identifiers.
  - 5,000 Unicode code points pass and 5,001 fail; byte length must not be used.
- [ ] Run go test ./... and confirm the new tests fail because the API does not exist.
- [ ] Implement Request, Result, ProviderResult, Provider, and constants AutoLanguage, AutoProvider, UndeterminedLanguage, and DefaultMaxTextRunes.
- [ ] Implement explicit BCP 47-style validation for this release using:
  - two- or three-letter primary language;
  - optional four-letter script;
  - optional two-letter or three-digit region;
  - canonical lowercase language, title-case script, and uppercase region.
- [ ] Keep normalization deterministic and table-independent; vendor support stays in each adapter.
- [ ] Define all eight ErrorKind values from the design, Error with Error and Unwrap methods, IsKind, and a public NewProviderError helper for custom adapters.
- [ ] Define ProviderFailure and AggregateError records so callers can inspect ordered failures. Error strings may include provider, kind, and status, but never Cause text or request/response content.
- [ ] Add tests proving errors.Is/errors.As work, IsKind works, aggregate ordering is preserved, and secret-looking Cause text is absent from Error strings.
- [ ] Run go test ./... and confirm all contract tests pass.
- [ ] Commit:

~~~bash
git add request.go request_test.go result.go provider.go language.go language_test.go errors.go errors_test.go
git commit -m "feat: define public translation contracts"
~~~

## Task 3: Construct clients and support explicit provider selection

**Files:**
- Create: options.go
- Create: hook.go
- Create: client.go
- Create: client_test.go

- [ ] Create reusable fakeProvider test doubles with name, Supports callback, Translate callback, and an atomic call counter.
- [ ] Write failing constructor tests for:
  - no providers;
  - nil providers;
  - duplicate normalized names;
  - invalid provider identifiers;
  - non-positive timeout, attempt timeout, and text limit;
  - immutable registration order after New returns.
- [ ] Write failing explicit-selection tests for:
  - exactly one named provider is invoked;
  - unknown provider returns ErrorUnknownProvider;
  - unsupported pair returns ErrorUnsupportedLanguage without calling Translate;
  - explicit calls receive the total remaining deadline;
  - caller cancellation wins over the client timeout.
- [ ] Implement WithProviders, WithTimeout, WithAttemptTimeout, WithMaxTextRunes, and WithAttemptHook.
- [ ] Use defaults of 15 seconds total, 3 seconds per non-final attempt, and 5,000 runes.
- [ ] Copy option inputs into private client state. Client and provider configurations must not mutate after construction.
- [ ] Implement validation before routing, explicit selection, provider support checks, context deadline composition, and result assembly.
- [ ] Normalize successful output: reject empty translated text as ErrorProviderFailure; canonicalize detected source when possible; use und when detection is absent.
- [ ] Run go test ./... and go test -race ./...; confirm explicit routing and concurrent calls pass.
- [ ] Commit:

~~~bash
git add options.go hook.go client.go client_test.go
git commit -m "feat: add client construction and explicit routing"
~~~

## Task 4: Implement serial automatic fallback and hooks

**Files:**
- Modify: client.go
- Modify: client_test.go
- Modify: hook.go
- Modify: errors.go
- Modify: errors_test.go

- [ ] Write failing auto-routing tests that prove:
  - providers are attempted strictly in registration order;
  - unsupported providers are skipped without a call;
  - rate limit, timeout, unavailable, 5xx, and transport/provider failures marked retryable advance to the next provider;
  - invalid request, authentication, unsupported language, and every non-retryable failure stop immediately;
  - an untyped provider error becomes retryable ErrorProviderFailure;
  - no provider starts after the total context expires;
  - all failures return an AggregateError in attempted order;
  - zero eligible providers returns ErrorUnsupportedLanguage.
- [ ] Add deadline tests with controllable fake providers and generous margins:
  - each non-final eligible provider receives at most 3 seconds or remaining total time;
  - the final eligible provider receives the remainder;
  - explicit routing is never capped to 3 seconds.
- [ ] Define Attempt with Provider, Duration, Success, and ErrorKind only, plus AttemptHook.
- [ ] Add tests that the hook fires once per completed attempt, never includes payload text, runs synchronously, and cannot break translation when it panics.
- [ ] Implement the serial loop without goroutines. Determine the eligible-provider list before assigning which attempt is final.
- [ ] Before every call, check the total context. Derive and cancel an attempt context immediately after each provider returns.
- [ ] Normalize context cancellation/deadline failures to ErrorTimeout while retaining the cause for errors.Is/errors.As.
- [ ] Invoke hooks through a small recovery boundary after each attempt; do not invoke hooks for skipped providers.
- [ ] Run go test ./... and go test -race ./....
- [ ] Commit:

~~~bash
git add client.go client_test.go hook.go errors.go errors_test.go
git commit -m "feat: add serial provider fallback"
~~~

## Task 5: Add bounded HTTP utilities and Google Cloud Translation

**Files:**
- Create: internal/httpjson/httpjson.go
- Create: internal/httpjson/httpjson_test.go
- Create: providers/google/google.go
- Create: providers/google/languages.go
- Create: providers/google/google_test.go

- [ ] Write failing httpjson tests for JSON request encoding, response status/body capture, malformed JSON, a 1 MiB response limit, cancellation, and custom client transport.
- [ ] Implement a small internal helper that:
  - creates requests with context;
  - sets JSON headers;
  - limits response reads to 1 MiB;
  - drains at most 4 KiB before closing;
  - never mutates http.DefaultClient or http.DefaultTransport;
  - returns bounded status/body information for adapter-specific mapping.
- [ ] Write failing Google adapter tests against httptest.Server:
  - constructor rejects missing API key and invalid base URL;
  - Name returns google;
  - Supports follows explicit source/target maps and accepts auto only as source;
  - POST path is /language/translate/v2;
  - API key is the key query parameter;
  - JSON includes q, target, format text, and omits source for auto;
  - success parses translatedText and detectedSourceLanguage;
  - HTML entities in translatedText are decoded once;
  - 400, 401/403, 429, 5xx, malformed JSON, empty translations, transport errors, and cancellation map to the expected LingoMux errors.
- [ ] Implement google.Config with APIKey, BaseURL, and HTTPClient. Default BaseURL is https://translation.googleapis.com.
- [ ] Implement explicit canonical-to-Google and Google-to-canonical language maps, including the common mapping set where Google supports it.
- [ ] Implement New(Config) (*Provider, error), with *Provider satisfying lingomux.Provider, plus Name, Supports, and Translate using Cloud Translation Basic v2 REST.
- [ ] Do not retry inside the adapter and do not include Google response bodies in returned error text.
- [ ] Run go test ./... and go test -race ./....
- [ ] Commit:

~~~bash
git add internal/httpjson providers/google
git commit -m "feat: add Google translation provider"
~~~

## Task 6: Add Microsoft Azure AI Translator

**Files:**
- Create: providers/microsoft/microsoft.go
- Create: providers/microsoft/languages.go
- Create: providers/microsoft/microsoft_test.go

- [ ] Write failing httptest tests for:
  - required API key and valid endpoint;
  - optional Region header;
  - POST /translate with api-version=3.0, to, and optional from query values;
  - JSON body containing one text object;
  - auto detection through detectedLanguage;
  - explicit mappings for zh-CN/zh-TW and other supported common tags;
  - successful translation selection;
  - 400, 401/403, 408, 429, 5xx, malformed/empty responses, transport errors, and cancellation.
- [ ] Implement microsoft.Config with APIKey, Region, Endpoint, and HTTPClient. Default Endpoint is https://api.cognitive.microsofttranslator.com.
- [ ] Implement New(Config) (*Provider, error), with *Provider satisfying lingomux.Provider.
- [ ] Implement explicit bidirectional mapping tables; never silently collapse Chinese or Portuguese variants.
- [ ] Set Ocp-Apim-Subscription-Key and, when configured, Ocp-Apim-Subscription-Region. Never place credentials in errors.
- [ ] Map Azure status codes into normalized errors and retryability, with throttling and transient server errors retryable.
- [ ] Run go test ./... and go test -race ./....
- [ ] Commit:

~~~bash
git add providers/microsoft
git commit -m "feat: add Microsoft translation provider"
~~~

## Task 7: Add DeepL

**Files:**
- Create: providers/deepl/deepl.go
- Create: providers/deepl/languages.go
- Create: providers/deepl/deepl_test.go

- [ ] Write failing httptest tests for:
  - required API key and valid base URL;
  - paid default https://api.deepl.com and configurable Free endpoint https://api-free.deepl.com;
  - POST /v2/translate;
  - Authorization: DeepL-Auth-Key header;
  - JSON text array with one element, target_lang, and omitted source_lang for auto;
  - separate source and target maps for DeepL's documented language variants;
  - detected_source_language and text parsing;
  - 400, 403, 404, 429, 456 quota exhaustion, 5xx, malformed/empty responses, transport errors, and cancellation.
- [ ] Implement deepl.Config with APIKey, BaseURL, and HTTPClient.
- [ ] Implement New(Config) (*Provider, error), with *Provider satisfying lingomux.Provider.
- [ ] Implement explicit source and target maps because DeepL accepts different variants depending on direction. Supports must return false for unsupported pairs without guessing.
- [ ] Treat 429, DeepL 456 quota exhaustion, and transient 5xx as retryable; map 456 to ErrorRateLimited. Treat authentication and invalid endpoint/request failures as non-retryable.
- [ ] Never log or surface the Authorization value or response body.
- [ ] Run go test ./... and go test -race ./....
- [ ] Commit:

~~~bash
git add providers/deepl
git commit -m "feat: add DeepL translation provider"
~~~

## Task 8: Add the OpenAI Responses API adapter

**Files:**
- Create: providers/openai/openai.go
- Create: providers/openai/languages.go
- Create: providers/openai/openai_test.go

- [ ] Write failing httptest tests for:
  - required API key and explicitly required model;
  - valid configurable base URL;
  - a BaseURL ending in /v1 and POST to its /responses endpoint, producing /v1/responses;
  - Bearer authorization and JSON content type;
  - request includes model, store false, max_output_tokens, fixed instructions, and one input string;
  - instructions request translated text only and preservation of URLs, mentions, emoji, whitespace, and formatting;
  - explicit source and auto source produce deterministic instructions without interpolating untrusted text into instructions;
  - parsing output message content entries of type output_text;
  - unrelated output entries are ignored;
  - empty or structurally invalid output is ErrorProviderFailure;
  - 400, 401/403, 408, 429, 5xx, malformed JSON, transport errors, and cancellation.
- [ ] Implement openai.Config with APIKey, Model, BaseURL, MaxOutputTokens, and HTTPClient. Default BaseURL is https://api.openai.com/v1 and default MaxOutputTokens is 2048.
- [ ] Implement New(Config) (*Provider, error), with *Provider satisfying lingomux.Provider.
- [ ] Implement the Responses REST payload directly with no SDK, tools, background mode, conversation state, or implicit model choice.
- [ ] Use canonical language display names from an explicit table when building instructions. Supports must reject unmapped tags.
- [ ] Return und for auto source because this adapter does not expose reliable source detection; preserve an explicit canonical source.
- [ ] Map authentication, throttling, timeout, transient server, and other failures to the common error model without returning raw model output in errors.
- [ ] Run go test ./... and go test -race ./....
- [ ] Commit:

~~~bash
git add providers/openai
git commit -m "feat: add OpenAI translation provider"
~~~

## Task 9: Prove external extensibility and document usage

**Files:**
- Create: lingomux_external_test.go
- Create: examples/basic/main.go
- Create: README.md
- Modify: doc.go as needed

- [ ] Write a black-box test in package lingomux_test with a custom provider declared outside the root package. Prove it can be registered, selected explicitly, used in auto mode, and inspected through public errors without internal imports.
- [ ] Run that test first and fix only public-API gaps required for genuine third-party implementation.
- [ ] Add a compiling basic example that first checks each provider constructor error, then passes the four provider instances to WithProviders in Google, Microsoft, DeepL, OpenAI order and translates one text segment. Use placeholder variables, never working keys.
- [ ] Document:
  - installation and minimum Go version;
  - the library-not-service boundary;
  - one-segment Translate usage;
  - explicit versus auto routing;
  - 15-second total and 3-second non-final attempt defaults;
  - fallback and non-retryable error behavior;
  - supported canonical language format and provider-specific support;
  - Google, Microsoft, DeepL Free/paid, and OpenAI configuration;
  - custom Provider implementation;
  - concurrency guarantees;
  - privacy rules and hook restrictions;
  - all non-goals from the design.
- [ ] Confirm public examples use github.com/Sakuragi27/lingomux imports and compile with go test ./....
- [ ] Commit:

~~~bash
git add lingomux_external_test.go examples/basic/main.go README.md doc.go
git commit -m "docs: add usage and extensibility examples"
~~~

## Task 10: Final verification and release-readiness review

**Files:**
- Modify only files required by failures found below.

- [ ] Format all packages:

~~~bash
go fmt ./...
~~~

- [ ] Run the full unit and black-box suite:

~~~bash
go test ./...
~~~

- [ ] Run race detection:

~~~bash
go test -race ./...
~~~

- [ ] Run static analysis and dependency inspection:

~~~bash
go vet ./...
go list -m all
~~~

Expected dependency inspection result: only the module itself and the Go standard library.

- [ ] Check patch hygiene:

~~~bash
git diff --check
git status --short
~~~

- [ ] Audit exported identifiers for doc comments and verify root lingomux does not import any providers subpackage.
- [ ] Search the repository for credential-shaped literals, raw response logging, TODO/FIXME markers, live vendor URLs in tests, and accidental batch/service APIs.
- [ ] Cross-check every acceptance criterion in the design: four providers, public extensibility, explicit selection, serial fallback, default order through registration, retryability policy, 15-second total deadline, 3-second non-final cap, one text segment, typed errors, canonical languages, no logging, and concurrent safety.
- [ ] If verification causes changes, rerun every command above and commit only the verified fixes:

~~~bash
git add -A
git commit -m "test: verify LingoMux release readiness"
~~~

## Acceptance checklist

- [ ] A business application can import the module and translate one segment without running a separate service.
- [ ] Google, Microsoft, DeepL, and OpenAI work through their documented REST APIs.
- [ ] Explicit provider selection never falls back.
- [ ] Auto mode attempts only eligible providers, strictly serially and in registration order.
- [ ] The documented default/recommended order is Google, Microsoft, DeepL, OpenAI.
- [ ] Retryable failures may fall back; configuration and request errors stop.
- [ ] Caller cancellation and the 15-second total deadline propagate to HTTP calls.
- [ ] No source text, output text, key, token, or raw body appears in errors or hooks.
- [ ] External packages can implement Provider without internal dependencies.
- [ ] go test, go test -race, go vet, formatting, and diff checks all pass.


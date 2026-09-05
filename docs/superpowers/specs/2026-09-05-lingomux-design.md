# LingoMux Go Library Design

Date: 2026-09-05

## Overview

LingoMux is a Go library that gives a business application one translation API while routing requests to multiple third-party translation providers. The first release supports Google Cloud Translation, Microsoft Azure AI Translator, DeepL, and OpenAI.

The business application imports LingoMux directly. LingoMux is not an HTTP service and does not own deployment, authentication, persistence, message lifecycle, or IM-specific behavior.

## Goals

- Provide one stable Go API for translating one text segment per call.
- Allow callers to select a provider explicitly or use ordered automatic fallback.
- Ship built-in providers for Google, Microsoft, DeepL, and OpenAI.
- Make third-party providers implementable outside this repository.
- Normalize provider language codes, results, and errors.
- Enforce a 15-second default total deadline while respecting caller cancellation.
- Keep provider configuration instance-scoped and safe for concurrent use.
- Keep the core package small and independent of vendor SDKs.

## Non-goals

- HTTP or RPC endpoints.
- IM message storage, editing, deletion, delivery, or user-language management.
- Batch, document, image, speech, or streaming translation.
- Parallel provider racing.
- Billing, quotas, dashboards, persistent caches, or distributed coordination.
- Automatic translation-quality scoring.
- Runtime loading of compiled plugins.

## Public API

The root package is named `lingomux`. It owns the stable contracts and routing behavior. Built-in provider implementations live in subpackages so applications import only the providers they use.

```go
client, err := lingomux.New(
    lingomux.WithProviders(
        google.New(google.Config{APIKey: googleKey}),
        microsoft.New(microsoft.Config{
            APIKey: microsoftKey,
            Region: microsoftRegion,
        }),
        deepl.New(deepl.Config{APIKey: deeplKey}),
        openai.New(openai.Config{
            APIKey: openAIKey,
            Model:  openAIModel,
        }),
    ),
    lingomux.WithTimeout(15*time.Second),
)
if err != nil {
    return err
}

result, err := client.Translate(ctx, lingomux.Request{
    Text:           "Hello",
    SourceLanguage: lingomux.AutoLanguage,
    TargetLanguage: "zh-CN",
    Provider:       lingomux.AutoProvider,
})
```

The main types are:

```go
const (
    AutoLanguage = "auto"
    AutoProvider = "auto"
)

type Request struct {
    Text           string
    SourceLanguage string
    TargetLanguage string
    Provider       string
}

type Result struct {
    Text           string
    SourceLanguage string
    TargetLanguage string
    Provider       string
    Duration       time.Duration
}

type ProviderResult struct {
    Text           string
    SourceLanguage string
}

type Provider interface {
    Name() string
    Supports(sourceLanguage, targetLanguage string) bool
    Translate(context.Context, Request) (ProviderResult, error)
}

func New(options ...Option) (*Client, error)
func (c *Client) Translate(ctx context.Context, request Request) (Result, error)
```

`Client` and built-in providers are safe for concurrent use after construction. Configuration is immutable after `New` returns.

## Provider registration and selection

`WithProviders` requires at least one non-nil provider and rejects duplicate provider names. Names are lowercase ASCII identifiers. Registration order defines fallback order. The recommended initial order is:

1. `google`
2. `microsoft`
3. `deepl`
4. `openai`

When `Request.Provider` is empty or `auto`, LingoMux walks this list serially. Providers that report an unsupported language pair are skipped without making a network request.

When a concrete provider name is requested, only that provider is called. An unknown or unsupported provider produces a typed error and never falls back.

## Timeout and cancellation

The client default total timeout is 15 seconds and is configurable with `WithTimeout`. A caller deadline shorter than the configured timeout always wins. Cancellation propagates into every outbound HTTP request.

For automatic routing, each non-final provider gets at most three seconds or the remaining total time, whichever is shorter. The final provider receives the remaining deadline. With the default provider order and a 15-second total timeout, OpenAI can receive approximately six seconds after three preceding timeouts.

The router does not start a provider call after the total context has expired. Explicit single-provider requests receive the entire remaining deadline.

There is no retry inside the core router or built-in providers. Moving to the next provider is the retry strategy.

## Error model and fallback policy

LingoMux exposes a typed error with these kinds:

```go
type ErrorKind string

const (
    ErrorInvalidRequest      ErrorKind = "invalid_request"
    ErrorUnknownProvider     ErrorKind = "unknown_provider"
    ErrorUnsupportedLanguage ErrorKind = "unsupported_language"
    ErrorAuthentication      ErrorKind = "authentication"
    ErrorRateLimited         ErrorKind = "rate_limited"
    ErrorTimeout             ErrorKind = "timeout"
    ErrorUnavailable         ErrorKind = "unavailable"
    ErrorProviderFailure     ErrorKind = "provider_failure"
)

type Error struct {
    Kind       ErrorKind
    Provider   string
    StatusCode int
    Retryable  bool
    Cause      error
}
```

The error implements `error` and `Unwrap`. Helper functions allow callers to inspect kinds without parsing strings.

Automatic fallback occurs only for retryable failures: rate limiting, timeouts, temporary unavailability, provider `5xx` responses, and transport failures. Invalid requests, authentication failures, and unsupported-language errors are returned immediately. This makes configuration problems visible instead of silently consuming another provider.

If every eligible provider fails, the router returns an aggregate error containing the ordered provider failures. It does not include source text, translated text, or API keys.

## Request validation

The core validates before selecting a provider:

- Text must contain at least one non-whitespace character.
- Text must not exceed 5,000 Unicode code points by default.
- Target language is required and cannot be `auto`.
- Source language defaults to `auto` when empty.
- Provider defaults to `auto` when empty.
- Source and target language tags must be syntactically valid BCP 47-style tags. Actual language-pair support remains provider-specific.

The maximum text length is configurable. LingoMux preserves the original text exactly and does not trim, escape, segment, or otherwise rewrite it.

## Language normalization

The public API uses canonical BCP 47-style language tags such as `en`, `zh-CN`, `zh-TW`, `ja`, and `pt-BR`, plus `auto` for source-language detection.

Each provider adapter owns a table that converts canonical LingoMux tags to vendor-specific codes. It also converts a detected source language back to the canonical representation. Provider-specific language identifiers never escape through the root API.

The first release uses explicit mapping tables rather than guessing or automatically stripping regions. This avoids silently turning Traditional Chinese into Simplified Chinese or choosing the wrong Portuguese variant. Its guaranteed common mapping set is `en`, `zh-CN`, `zh-TW`, `ja`, `ko`, `fr`, `de`, `es`, `pt`, `pt-BR`, `it`, `ru`, `ar`, and `hi`; adapters can expose additional documented mappings.

## Built-in providers

All built-in adapters use `net/http` directly. This keeps dependency weight low and makes timeout, error handling, mocking, and transport customization consistent. Every provider config accepts an optional `*http.Client`; when omitted, a safe shared client is created without using `http.DefaultClient` mutation.

### Google

- Uses the Google Cloud Translation Basic v2 REST API with an API key.
- Supports source-language auto-detection.
- Converts Google response and error payloads into LingoMux types.
- Allows a configurable base URL for tests and compatible gateways.

### Microsoft

- Uses the Azure AI Translator text REST API.
- Accepts subscription key, region, and optional endpoint.
- Supports source-language auto-detection.
- Treats Azure throttling and transient server responses as retryable.

### DeepL

- Uses the DeepL text translation REST API.
- Accepts an API key and an optional endpoint, supporting Free and paid endpoints.
- Maps DeepL language variants explicitly.
- Supports source-language auto-detection by omitting the source language when requested.

### OpenAI

- Uses the OpenAI Responses API through REST.
- Requires an explicitly configured model; LingoMux does not silently select one.
- Supports a configurable base URL for OpenAI-compatible gateways that implement the required endpoint semantics.
- Uses a fixed translation instruction that requests only the translated text and preserves URLs, mentions, emoji, whitespace, and formatting.
- Rejects empty or structurally invalid model output as a provider failure.

API keys and credentials are passed into provider constructors. The library does not read environment variables, configuration files, or global process state.

## Package structure

```text
lingomux/
├── client.go
├── errors.go
├── language.go
├── options.go
├── provider.go
├── request.go
├── result.go
├── internal/
│   └── httputil/
├── providers/
│   ├── google/
│   ├── microsoft/
│   ├── deepl/
│   └── openai/
├── examples/
│   └── basic/
└── docs/
```

The root package never imports provider subpackages. Provider subpackages import the root package to implement its public interface, preventing import cycles and allowing consumers to supply their own implementations.

## Observability and privacy

The library does not log by default. Applications can install optional hooks for attempt completion and final result reporting. Hook payloads contain provider name, duration, success, and normalized error kind, but never contain request text, translated text, credentials, or raw provider response bodies.

Hooks run synchronously after an attempt and must be documented as performance-sensitive. A panic in an application hook is recovered so it cannot break translation.

## Testing strategy

- Table-driven unit tests cover validation, language normalization, provider selection, fallback rules, and aggregate errors.
- Fake providers cover ordering, explicit selection, unsupported pairs, cancellation, and concurrent use.
- Each built-in adapter uses `httptest.Server` fixtures for successful responses and every recognized error class.
- Deadline tests use controllable fake providers and generous timing margins to avoid flaky wall-clock assertions.
- Race tests run with `go test -race ./...`.
- No automated test sends traffic to a real translation vendor.
- Examples compile as part of `go test ./...`.

## Delivery criteria for the first release

The first release is complete when:

- One imported client can translate a single text segment through all four built-in providers.
- Explicit provider selection and ordered automatic fallback behave as documented.
- A total 15-second timeout and caller cancellation stop outbound work.
- Language mappings cover the guaranteed common set documented above, including explicit Simplified and Traditional Chinese variants.
- All normalized error kinds have unit coverage.
- A third-party provider can be implemented using only the public root-package contracts.
- `go test ./...`, `go test -race ./...`, and `go vet ./...` pass.
- Package documentation and a runnable basic example show configuration and use without exposing credentials.

## Future extensions

The public contracts leave room for additional providers, a cache decorator, metrics hooks, circuit-breaker decorators, glossary options, and a separate HTTP service wrapper. These are not part of the first release and must not complicate its core API.

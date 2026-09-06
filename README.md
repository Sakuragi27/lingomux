# LingoMux

LingoMux is an embeddable Go translation-routing library. An application calls it directly to translate exactly one text segment per `Translate` call. It is not an HTTP or RPC service and does not own deployment, application authentication, persistence, or message lifecycle.

The built-in adapters use the REST APIs for Google Cloud Translation Basic v2, Microsoft Azure AI Translator, DeepL, and OpenAI Responses. The root package also accepts providers implemented outside this repository.

## Requirements and installation

LingoMux requires Go 1.26 or newer.

Until the first release is tagged, install the current implementation from its feature branch:

```sh
go get github.com/Sakuragi27/lingomux@feature/lingomux-library
```

After it is merged, the default branch can be installed with:

```sh
go get github.com/Sakuragi27/lingomux@main
```

For normal application use, prefer a tagged version. After `v0.1.0` is published:

```sh
go get github.com/Sakuragi27/lingomux@v0.1.0
```

Application imports always use `github.com/Sakuragi27/lingomux` (and its provider package paths); the branch, tag, commit, or generated pseudo-version is recorded in the application go.mod.

The module uses only the Go standard library.

## Basic use

See [`examples/basic`](examples/basic/main.go) for a compiling four-provider program. It reads credentials and the OpenAI model from environment variables, checks every constructor error, then registers providers in the recommended order:

1. Google
2. Microsoft
3. DeepL
4. OpenAI

Registration order is automatic-routing order. Putting Google first is how an application makes Google its default first choice; the root package does not import provider subpackages or silently construct a default provider.

The essential call translates one segment:

```go
result, err := client.Translate(ctx, lingomux.Request{
    Text:           "Hello",
    SourceLanguage: lingomux.AutoLanguage,
    TargetLanguage: "zh-CN",
    Provider:       lingomux.AutoProvider,
})
if err != nil {
    return err
}
fmt.Println(result.Text)
```

Text must contain a non-whitespace character and is limited to 5,000 Unicode code points by default. Change that limit with `WithMaxTextRunes`. LingoMux preserves the supplied text; it does not trim, escape, split, or otherwise rewrite it.

## Routing, fallback, and timeouts

Set `Request.Provider` to a registered provider name such as `"deepl"` for explicit routing. Explicit routing calls only that provider, gives it the remaining total deadline, and never falls back. An empty provider or `lingomux.AutoProvider` selects automatic routing.

Auto routing first asks each registered provider whether it `Supports` the canonical source/target pair. It skips unsupported providers, then attempts eligible providers serially in registration order. It never races providers in parallel.

The default total timeout for one `Translate` call is 15 seconds. Each non-final auto attempt receives at most 3 seconds or the remaining total time, whichever is shorter; the final eligible provider receives the remaining total time. Configure these limits with `WithTimeout` and `WithAttemptTimeout`. A shorter caller deadline or caller cancellation always wins and propagates to the provider request.

There are no retries inside the router or built-in adapters. Moving to the next eligible provider is the retry strategy, and happens only when an error is marked retryable. Built-in retryable cases are rate limiting, timeout, temporary unavailability, `5xx` responses, transport failures, and provider failures that may be transient. Invalid requests, authentication failures, unsupported languages, and other non-retryable failures stop auto routing immediately. An untyped custom-provider error is normalized to a retryable `ErrorProviderFailure`; custom providers should use `NewProviderError` when they know the precise kind and retryability.

When all eligible providers fail retryably, `Translate` returns an `*lingomux.AggregateError` whose exported `Failures` preserve attempt order. Use `errors.As`, `errors.Is`, or `lingomux.IsKind` rather than parsing error strings:

```go
var aggregate *lingomux.AggregateError
if errors.As(err, &aggregate) {
    for _, failure := range aggregate.Failures {
        var typed *lingomux.Error
        if errors.As(failure.Error, &typed) {
            fmt.Printf("%s: %s (retryable=%t)\n", failure.Provider, typed.Kind, typed.Retryable)
        }
    }
}
```

## Languages

The public API accepts a deliberately small BCP 47-style syntax: a two- or three-letter language, optionally a four-letter script, and optionally a two-letter or three-digit region. It canonicalizes casing (`EN` to `en`, `zh-hant-tw` to `zh-Hant-TW`). `auto` is valid only as a source language; when a provider cannot report an auto-detected source, the result contains `und`.

Syntax validity does not imply provider support. Adapters use explicit mappings and do not guess by stripping scripts or regions. `Provider.Supports(source, target)` is the authoritative runtime check. Current built-in mappings are listed below. This is the language set implemented by LingoMux, not the complete language catalog advertised by each vendor:

| Provider | Canonical tags accepted by `Supports` |
| --- | --- |
| Google | `ar`, `de`, `en`, `es`, `fr`, `hi`, `id`, `it`, `ja`, `ko`, `pt`, `pt-BR`, `ru`, `th`, `vi`, `zh-CN`, `zh-TW` |
| Microsoft | `ar`, `de`, `en`, `es`, `fr`, `hi`, `id`, `it`, `ja`, `ko`, `pt`, `pt-BR`, `pt-PT`, `ru`, `th`, `vi`, `zh-CN`, `zh-TW` |
| DeepL | `ar`, `bg`, `cs`, `da`, `de`, `el`, `en`, `es`, `et`, `fi`, `fr`, `he`, `hi`, `hu`, `id`, `it`, `ja`, `ko`, `lt`, `lv`, `nb`, `nl`, `pl`, `pt`, `pt-BR`, `ro`, `ru`, `sk`, `sl`, `sv`, `th`, `tr`, `uk`, `vi`, `zh-CN`, `zh-TW` |
| OpenAI | `ar`, `bg`, `cs`, `da`, `de`, `el`, `en`, `es`, `et`, `fi`, `fr`, `he`, `hi`, `hu`, `id`, `it`, `ja`, `ko`, `lt`, `lv`, `nb`, `nl`, `pl`, `pt`, `pt-BR`, `ro`, `ru`, `sk`, `sl`, `sv`, `th`, `tr`, `uk`, `vi`, `zh-CN`, `zh-TW` |

`auto` is additionally accepted as the source by all four built-in adapters. DeepL keeps distinct source and target maps; for example, it maps regional Chinese and Portuguese source tags to the generic vendor source codes while preserving supported target variants. The providers do not all support every syntactically valid canonical tag or the same set of variants.

## Provider configuration

### Credential ownership

Credentials belong to the calling application. Read them from environment variables, a configuration center, or a secret manager, then pass them once when constructing each provider. Do not hard-code credentials or commit them to source control.

```go
googleProvider, err := google.New(google.Config{
    APIKey: os.Getenv("GOOGLE_TRANSLATE_API_KEY"),
})
if err != nil {
    return err
}

client, err := lingomux.New(lingomux.WithProviders(googleProvider))
if err != nil {
    return err
}

result, err := client.Translate(ctx, lingomux.Request{
    Text:           message,
    SourceLanguage: lingomux.AutoLanguage,
    TargetLanguage: "zh-CN",
})
```

LingoMux never reads environment variables, configuration files, or global process state, and it does not persist credentials. A key is not passed on each `Translate` call. Constructed clients and built-in providers can be reused concurrently; to rotate a key, construct a new provider and client, then swap them at the application boundary.

Every built-in config also accepts an optional `*http.Client` for transport control and testing.

### Google Cloud Translation

```go
provider, err := google.New(google.Config{
    APIKey:  googleKey,
    BaseURL: "https://translation.googleapis.com", // optional; this is the default
})
```

The adapter calls Cloud Translation Basic v2 and supports source auto-detection. `APIKey` is required.

### Microsoft Azure AI Translator

```go
provider, err := microsoft.New(microsoft.Config{
    APIKey:   microsoftKey,
    Region:   microsoftRegion,
    Endpoint: "https://api.cognitive.microsofttranslator.com", // optional default
})
```

`APIKey` is required. Configure `Region` when the Azure resource requires the subscription-region header. `Endpoint` can select a compatible or resource-specific endpoint.

### DeepL

```go
// DeepL paid: BaseURL may be omitted; it defaults to https://api.deepl.com.
paid, err := deepl.New(deepl.Config{APIKey: deepLKey})

// DeepL API Free must select the Free endpoint explicitly.
free, err := deepl.New(deepl.Config{
    APIKey:  deepLFreeKey,
    BaseURL: "https://api-free.deepl.com",
})
```

`APIKey` is required. Always check the constructor error for either account type.

### OpenAI

```go
provider, err := openai.New(openai.Config{
    APIKey:          openAIKey,
    Model:           openAIModel, // required: there is no implicit model
    MaxOutputTokens: 2048,        // optional; 2048 is the zero-value default
    BaseURL:         "https://api.openai.com/v1", // optional default
})
```

The adapter sends one stateless Responses API request, sets `store` to `false`, and does not use conversation state, tools, or background mode. The input segment is still sent to the configured API endpoint, so applications remain responsible for deciding whether that provider and its data-handling terms are appropriate.

## Custom providers

Implement the public `lingomux.Provider` interface; no `internal` package is needed:

```go
type Provider interface {
    Name() string
    Supports(sourceLanguage, targetLanguage string) bool
    Translate(context.Context, lingomux.Request) (lingomux.ProviderResult, error)
}
```

Provider names are normalized to lowercase and must begin with an ASCII letter, followed only by ASCII letters, digits, `_`, or `-`; `auto` is reserved. Names must be unique after normalization. `Supports` receives canonicalized language tags. `Translate` must honor its context and return non-empty translated text. For classified failures, return `lingomux.NewProviderError(kind, name, statusCode, retryable, cause)`; error messages should not expose input, output, credentials, or raw response bodies.

Register a custom implementation exactly like a built-in one:

```go
client, err := lingomux.New(lingomux.WithProviders(myProvider))
```

The external-package test in [`lingomux_external_test.go`](lingomux_external_test.go) is a complete local example of explicit routing, automatic fallback, and typed aggregate error inspection.

## Concurrency, observability, and privacy

A constructed `Client` and all built-in providers are safe for concurrent use. Configuration is copied into immutable client/provider state at construction. A custom provider must itself be safe if the application uses the client concurrently.

LingoMux does not log by default. `WithAttemptHook` installs a callback containing only provider name, duration, success, and normalized error kind—never source text, translated text, credentials, or raw provider response bodies. Errors and aggregate summaries likewise omit those sensitive values, although an error cause remains available through Go's error unwrapping and must be handled accordingly by the application.

Attempt hooks run synchronously after completed attempts, delay the translation call, and consume its total timeout budget, so they must return quickly. They may run concurrently for concurrent `Translate` calls and must synchronize shared state. Hook panics are recovered and do not break translation. Skipped or never-started providers do not produce completed-attempt hooks.

## Non-goals

The first release intentionally does not provide:

- HTTP or RPC endpoints;
- IM message storage, editing, deletion, delivery, or user-language management;
- batch, document, image, speech, or streaming translation;
- parallel provider racing;
- billing, quotas, dashboards, persistent caches, or distributed coordination;
- automatic translation-quality scoring; or
- runtime loading of compiled plugins.

Possible future decorators or wrappers such as caching, metrics, circuit breakers, glossary options, and a separate service remain application concerns in this release.

## License

MIT. See [`LICENSE`](LICENSE).

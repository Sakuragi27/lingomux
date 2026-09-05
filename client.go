package lingomux

import (
	"context"
	"errors"
	"reflect"
	"time"
)

type registeredProvider struct {
	name     string
	provider Provider
}

// Client validates and routes translation requests to configured providers.
type Client struct {
	providers      []registeredProvider
	providerByName map[string]Provider
	timeout        time.Duration
	attemptTimeout time.Duration
	maxTextRunes   int
	attemptHook    AttemptHook
}

// New constructs an immutable, concurrency-safe translation client.
func New(options ...Option) (*Client, error) {
	config := clientConfig{
		timeout:        defaultTimeout,
		attemptTimeout: defaultAttemptTimeout,
		maxTextRunes:   DefaultMaxTextRunes,
	}
	for _, option := range options {
		if option == nil {
			return nil, invalidRequestError()
		}
		if err := option(&config); err != nil {
			return nil, err
		}
	}
	if len(config.providers) == 0 {
		return nil, invalidRequestError()
	}

	providers := make([]registeredProvider, 0, len(config.providers))
	providerByName := make(map[string]Provider, len(config.providers))
	for _, provider := range config.providers {
		if isNilProvider(provider) {
			return nil, invalidRequestError()
		}
		name, err := normalizeProviderName(provider.Name())
		if err != nil || name == AutoProvider {
			return nil, invalidRequestError()
		}
		if _, exists := providerByName[name]; exists {
			return nil, invalidRequestError()
		}
		providers = append(providers, registeredProvider{name: name, provider: provider})
		providerByName[name] = provider
	}

	return &Client{
		providers:      providers,
		providerByName: providerByName,
		timeout:        config.timeout,
		attemptTimeout: config.attemptTimeout,
		maxTextRunes:   config.maxTextRunes,
		attemptHook:    config.attemptHook,
	}, nil
}

// Translate validates request and routes it explicitly or by serial automatic fallback.
func (client *Client) Translate(ctx context.Context, request Request) (Result, error) {
	normalized, err := normalizeRequest(request, client.maxTextRunes)
	if err != nil {
		return Result{}, err
	}
	if ctx == nil {
		return Result{}, invalidRequestError()
	}
	if err := ctx.Err(); err != nil {
		return Result{}, timeoutError(normalized.Provider, err)
	}

	totalCtx, cancel := context.WithTimeout(ctx, client.timeout)
	defer cancel()
	if normalized.Provider == AutoProvider {
		return client.translateAuto(totalCtx, normalized)
	}

	provider, exists := client.providerByName[normalized.Provider]
	if !exists {
		return Result{}, &Error{Kind: ErrorUnknownProvider, Provider: normalized.Provider}
	}
	supported := provider.Supports(normalized.SourceLanguage, normalized.TargetLanguage)
	if err := totalCtx.Err(); err != nil {
		return Result{}, timeoutError(normalized.Provider, err)
	}
	if !supported {
		return Result{}, &Error{Kind: ErrorUnsupportedLanguage, Provider: normalized.Provider}
	}

	result, _, err := client.translateAttempt(totalCtx, provider, normalized, 0)
	return result, err
}

func (client *Client) translateAuto(ctx context.Context, request Request) (Result, error) {
	eligible := make([]registeredProvider, 0, len(client.providers))
	for _, provider := range client.providers {
		if err := ctx.Err(); err != nil {
			return Result{}, timeoutError(AutoProvider, err)
		}
		supported := provider.provider.Supports(request.SourceLanguage, request.TargetLanguage)
		if err := ctx.Err(); err != nil {
			return Result{}, timeoutError(AutoProvider, err)
		}
		if supported {
			eligible = append(eligible, provider)
		}
	}
	if len(eligible) == 0 {
		return Result{}, &Error{Kind: ErrorUnsupportedLanguage, Provider: AutoProvider}
	}

	failures := make([]ProviderFailure, 0, len(eligible))
	for index, provider := range eligible {
		if err := ctx.Err(); err != nil {
			return Result{}, timeoutError(AutoProvider, err)
		}
		var attemptTimeout time.Duration
		if index < len(eligible)-1 {
			attemptTimeout = client.attemptTimeout
		}
		request.Provider = provider.name
		result, attempted, err := client.translateAttempt(ctx, provider.provider, request, attemptTimeout)
		if err == nil {
			return result, nil
		}
		typed := err.(*Error)
		if !typed.Retryable {
			return Result{}, typed
		}
		if attempted {
			failures = append(failures, ProviderFailure{Provider: provider.name, Error: typed})
		} else if ctx.Err() != nil {
			return Result{}, typed
		}
	}
	return Result{}, &AggregateError{Failures: failures}
}

// translateAttempt reports whether Translate was called, so preflight timeouts
// never appear as completed attempts in hooks or aggregated failures.
func (client *Client) translateAttempt(ctx context.Context, provider Provider, request Request, timeout time.Duration) (Result, bool, error) {
	var attemptCtx context.Context
	var cancel context.CancelFunc
	if timeout > 0 {
		attemptCtx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		attemptCtx, cancel = context.WithCancel(ctx)
	}
	if err := attemptCtx.Err(); err != nil {
		cancel()
		return Result{}, false, timeoutError(request.Provider, err)
	}
	started := time.Now()
	providerResult, err := provider.Translate(attemptCtx, request)
	duration := time.Since(started)
	cancel()
	var failure *Error
	if err != nil {
		failure = normalizeProviderError(err, request.Provider)
	} else if providerResult.Text == "" {
		failure = &Error{
			Kind:      ErrorProviderFailure,
			Provider:  request.Provider,
			Retryable: true,
		}
	}
	attempt := Attempt{Provider: request.Provider, Duration: duration, Success: failure == nil}
	if failure != nil {
		attempt.ErrorKind = failure.Kind
	}
	client.attemptHook.invoke(attempt)
	if failure != nil {
		return Result{}, true, failure
	}

	sourceLanguage := UndeterminedLanguage
	if providerResult.SourceLanguage != "" {
		if canonical, normalizeErr := normalizeLanguage(providerResult.SourceLanguage, false); normalizeErr == nil {
			sourceLanguage = canonical
		}
	}

	return Result{
		Text:           providerResult.Text,
		SourceLanguage: sourceLanguage,
		TargetLanguage: request.TargetLanguage,
		Provider:       request.Provider,
		Duration:       duration,
	}, true, nil
}

func timeoutError(provider string, cause error) *Error {
	return &Error{
		Kind:      ErrorTimeout,
		Provider:  provider,
		Retryable: true,
		Cause:     cause,
	}
}

func normalizeProviderError(err error, provider string) *Error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return timeoutError(provider, err)
	}
	var typed *Error
	if errors.As(err, &typed) && typed != nil {
		providerName := typed.Provider
		if providerName == "" {
			providerName = provider
		}
		return &Error{
			Kind:       typed.Kind,
			Provider:   providerName,
			StatusCode: typed.StatusCode,
			Retryable:  typed.Retryable,
			Cause:      err,
		}
	}
	return &Error{
		Kind:      ErrorProviderFailure,
		Provider:  provider,
		Retryable: true,
		Cause:     err,
	}
}

func isNilProvider(provider Provider) bool {
	if provider == nil {
		return true
	}
	value := reflect.ValueOf(provider)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

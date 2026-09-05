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

// Translate validates request and routes an explicitly selected provider.
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

	started := time.Now()
	providerResult, err := provider.Translate(totalCtx, normalized)
	duration := time.Since(started)
	if err != nil {
		return Result{}, normalizeProviderError(err, normalized.Provider)
	}
	if providerResult.Text == "" {
		return Result{}, &Error{
			Kind:      ErrorProviderFailure,
			Provider:  normalized.Provider,
			Retryable: true,
		}
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
		TargetLanguage: normalized.TargetLanguage,
		Provider:       normalized.Provider,
		Duration:       duration,
	}, nil
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

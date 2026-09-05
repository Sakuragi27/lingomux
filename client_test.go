package lingomux

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeProvider struct {
	name        string
	supportsFn  func(sourceLanguage, targetLanguage string) bool
	translateFn func(context.Context, Request) (ProviderResult, error)
	calls       atomic.Int64
}

func (provider *fakeProvider) Name() string {
	return provider.name
}

func (provider *fakeProvider) Supports(sourceLanguage, targetLanguage string) bool {
	if provider.supportsFn == nil {
		return true
	}
	return provider.supportsFn(sourceLanguage, targetLanguage)
}

func (provider *fakeProvider) Translate(ctx context.Context, request Request) (ProviderResult, error) {
	provider.calls.Add(1)
	if provider.translateFn == nil {
		return ProviderResult{Text: "translated"}, nil
	}
	return provider.translateFn(ctx, request)
}

func TestNewRejectsMissingAndNilProviders(t *testing.T) {
	var typedNil *fakeProvider
	tests := []struct {
		name    string
		options []Option
	}{
		{name: "no options"},
		{name: "empty provider option", options: []Option{WithProviders()}},
		{name: "nil provider", options: []Option{WithProviders(nil)}},
		{name: "typed nil provider", options: []Option{WithProviders(typedNil)}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.options...)
			assertErrorKind(t, err, ErrorInvalidRequest)
		})
	}
}

func TestNewRejectsDuplicateNormalizedProviderNames(t *testing.T) {
	_, err := New(WithProviders(
		&fakeProvider{name: "Google"},
		&fakeProvider{name: "google"},
	))
	assertErrorKind(t, err, ErrorInvalidRequest)
}

func TestNewRejectsInvalidAndReservedProviderNames(t *testing.T) {
	for _, name := range []string{"", "1google", "google cloud", "gøøgle", AutoProvider} {
		t.Run(name, func(t *testing.T) {
			_, err := New(WithProviders(&fakeProvider{name: name}))
			assertErrorKind(t, err, ErrorInvalidRequest)
		})
	}
}

func TestNewRejectsNonPositiveOptionValues(t *testing.T) {
	provider := &fakeProvider{name: "provider"}
	tests := []struct {
		name   string
		option Option
	}{
		{name: "zero total timeout", option: WithTimeout(0)},
		{name: "negative total timeout", option: WithTimeout(-time.Second)},
		{name: "zero attempt timeout", option: WithAttemptTimeout(0)},
		{name: "negative attempt timeout", option: WithAttemptTimeout(-time.Second)},
		{name: "zero text limit", option: WithMaxTextRunes(0)},
		{name: "negative text limit", option: WithMaxTextRunes(-1)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(WithProviders(provider), tt.option)
			assertErrorKind(t, err, ErrorInvalidRequest)
		})
	}
}

func TestNewCopiesProviderRegistrationInputs(t *testing.T) {
	first := &fakeProvider{name: "first"}
	second := &fakeProvider{name: "second"}
	replacement := &fakeProvider{name: "replacement"}
	providers := []Provider{first, second}
	option := WithProviders(providers...)

	providers[0] = replacement
	client, err := New(option)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	providers[1] = replacement

	if len(client.providers) != 2 || client.providers[0].provider != first || client.providers[1].provider != second {
		t.Fatalf("client provider order changed after input mutation: %#v", client.providers)
	}
}

func TestNewAcceptsPositiveOptionsAndAttemptHook(t *testing.T) {
	client, err := New(
		WithProviders(&fakeProvider{name: "provider"}),
		WithTimeout(time.Second),
		WithAttemptTimeout(100*time.Millisecond),
		WithMaxTextRunes(10),
		WithAttemptHook(func(Attempt) {}),
	)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if client == nil {
		t.Fatal("New returned a nil client")
	}
}

func TestTranslateExplicitInvokesOnlyNamedProviderWithNormalizedRequest(t *testing.T) {
	first := &fakeProvider{name: "First"}
	var supportedSource, supportedTarget string
	var translated Request
	second := &fakeProvider{
		name: "Second",
		supportsFn: func(sourceLanguage, targetLanguage string) bool {
			supportedSource, supportedTarget = sourceLanguage, targetLanguage
			return true
		},
		translateFn: func(_ context.Context, request Request) (ProviderResult, error) {
			translated = request
			return ProviderResult{Text: "你好", SourceLanguage: "EN-us"}, nil
		},
	}
	client, err := New(WithProviders(first, second))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	result, err := client.Translate(context.Background(), Request{
		Text:           "Hello",
		SourceLanguage: "EN-us",
		TargetLanguage: "ZH-cn",
		Provider:       "SECOND",
	})
	if err != nil {
		t.Fatalf("Translate returned error: %v", err)
	}
	if first.calls.Load() != 0 || second.calls.Load() != 1 {
		t.Fatalf("provider calls = (%d, %d), want (0, 1)", first.calls.Load(), second.calls.Load())
	}
	if supportedSource != "en-US" || supportedTarget != "zh-CN" {
		t.Fatalf("Supports received (%q, %q), want (en-US, zh-CN)", supportedSource, supportedTarget)
	}
	if translated.Text != "Hello" || translated.SourceLanguage != "en-US" || translated.TargetLanguage != "zh-CN" || translated.Provider != "second" {
		t.Fatalf("Translate request = %#v, want normalized fields with original text", translated)
	}
	if result.Text != "你好" || result.SourceLanguage != "en-US" || result.TargetLanguage != "zh-CN" || result.Provider != "second" {
		t.Fatalf("Translate result = %#v", result)
	}
}

func TestTranslateExplicitRejectsUnknownProvider(t *testing.T) {
	provider := &fakeProvider{name: "known"}
	client, err := New(WithProviders(provider))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	_, err = client.Translate(context.Background(), Request{Text: "Hello", TargetLanguage: "fr", Provider: "missing"})
	assertErrorKind(t, err, ErrorUnknownProvider)
	if provider.calls.Load() != 0 {
		t.Fatalf("Translate called provider %d times, want 0", provider.calls.Load())
	}
	var typed *Error
	if !errors.As(err, &typed) || typed.Provider != "missing" {
		t.Fatalf("unknown-provider error = %#v, want provider missing", typed)
	}
}

func TestTranslateExplicitRejectsUnsupportedPairWithoutTranslate(t *testing.T) {
	provider := &fakeProvider{
		name:       "limited",
		supportsFn: func(_, _ string) bool { return false },
	}
	client, err := New(WithProviders(provider))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	_, err = client.Translate(context.Background(), Request{Text: "Hello", TargetLanguage: "fr", Provider: "limited"})
	assertErrorKind(t, err, ErrorUnsupportedLanguage)
	if provider.calls.Load() != 0 {
		t.Fatalf("Translate called unsupported provider %d times, want 0", provider.calls.Load())
	}
}

func TestTranslateValidatesBeforeProviderSelection(t *testing.T) {
	client, err := New(WithProviders(&fakeProvider{name: "known"}))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	_, err = client.Translate(context.Background(), Request{Text: " ", TargetLanguage: "fr", Provider: "missing"})
	assertErrorKind(t, err, ErrorInvalidRequest)
}

func TestTranslateExplicitReceivesTotalRemainingDeadline(t *testing.T) {
	var remaining time.Duration
	provider := &fakeProvider{
		name: "deadline",
		translateFn: func(ctx context.Context, _ Request) (ProviderResult, error) {
			deadline, ok := ctx.Deadline()
			if !ok {
				return ProviderResult{}, errors.New("provider context has no deadline")
			}
			remaining = time.Until(deadline)
			return ProviderResult{Text: "translated"}, nil
		},
	}
	client, err := New(
		WithProviders(provider),
		WithTimeout(2*time.Second),
		WithAttemptTimeout(20*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	_, err = client.Translate(context.Background(), Request{Text: "Hello", TargetLanguage: "fr", Provider: "deadline"})
	if err != nil {
		t.Fatalf("Translate returned error: %v", err)
	}
	if remaining < time.Second || remaining > 2100*time.Millisecond {
		t.Fatalf("provider deadline remaining = %v, want approximately 2s", remaining)
	}
}

func TestTranslateTotalDeadlineIncludesProviderSupportCheck(t *testing.T) {
	provider := &fakeProvider{
		name: "slow-support",
		supportsFn: func(_, _ string) bool {
			time.Sleep(80 * time.Millisecond)
			return true
		},
	}
	client, err := New(WithProviders(provider), WithTimeout(20*time.Millisecond))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	_, err = client.Translate(context.Background(), Request{Text: "Hello", TargetLanguage: "fr", Provider: "slow-support"})
	assertErrorKind(t, err, ErrorTimeout)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Translate error = %v, want context deadline cause", err)
	}
	if provider.calls.Load() != 0 {
		t.Fatalf("Translate called provider %d times after total timeout, want 0", provider.calls.Load())
	}
}

func TestTranslateCallerCancellationWinsOverClientTimeout(t *testing.T) {
	provider := &fakeProvider{
		name: "cancel",
		translateFn: func(ctx context.Context, _ Request) (ProviderResult, error) {
			return ProviderResult{}, ctx.Err()
		},
	}
	client, err := New(WithProviders(provider), WithTimeout(time.Hour))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = client.Translate(ctx, Request{Text: "Hello", TargetLanguage: "fr", Provider: "cancel"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Translate error = %v, want context.Canceled", err)
	}
}

func TestTranslateNormalizesProviderResult(t *testing.T) {
	tests := []struct {
		name       string
		provider   ProviderResult
		wantSource string
	}{
		{name: "canonical detected source", provider: ProviderResult{Text: "translated", SourceLanguage: "ZH-hant-tw"}, wantSource: "zh-Hant-TW"},
		{name: "absent detected source", provider: ProviderResult{Text: "translated"}, wantSource: UndeterminedLanguage},
		{name: "invalid detected source", provider: ProviderResult{Text: "translated", SourceLanguage: "vendor_code"}, wantSource: UndeterminedLanguage},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &fakeProvider{
				name:        "provider",
				translateFn: func(context.Context, Request) (ProviderResult, error) { return tt.provider, nil },
			}
			client, err := New(WithProviders(provider))
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}

			result, err := client.Translate(context.Background(), Request{Text: "Hello", TargetLanguage: "fr", Provider: "provider"})
			if err != nil {
				t.Fatalf("Translate returned error: %v", err)
			}
			if result.SourceLanguage != tt.wantSource {
				t.Fatalf("source language = %q, want %q", result.SourceLanguage, tt.wantSource)
			}
		})
	}
}

func TestTranslateRejectsEmptyProviderText(t *testing.T) {
	provider := &fakeProvider{
		name:        "empty",
		translateFn: func(context.Context, Request) (ProviderResult, error) { return ProviderResult{}, nil },
	}
	client, err := New(WithProviders(provider))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	_, err = client.Translate(context.Background(), Request{Text: "Hello", TargetLanguage: "fr", Provider: "empty"})
	assertErrorKind(t, err, ErrorProviderFailure)
	var typed *Error
	if !errors.As(err, &typed) || typed.Provider != "empty" {
		t.Fatalf("provider failure = %#v, want provider empty", typed)
	}
}

func TestTranslateNormalizesUntypedProviderErrorWithoutExposingCause(t *testing.T) {
	secret := errors.New("request=Hello raw-response=secret-token")
	provider := &fakeProvider{
		name: "unsafe",
		translateFn: func(context.Context, Request) (ProviderResult, error) {
			return ProviderResult{}, secret
		},
	}
	client, err := New(WithProviders(provider))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	_, err = client.Translate(context.Background(), Request{Text: "Hello", TargetLanguage: "fr", Provider: "unsafe"})
	var typed *Error
	if !errors.As(err, &typed) {
		t.Fatalf("Translate error type = %T, want *Error", err)
	}
	if typed.Kind != ErrorProviderFailure || typed.Provider != "unsafe" || !typed.Retryable {
		t.Fatalf("normalized provider error = %#v", typed)
	}
	if !errors.Is(err, secret) {
		t.Fatal("normalized provider error did not retain its cause")
	}
	if message := err.Error(); strings.Contains(message, "Hello") || strings.Contains(message, "secret-token") || strings.Contains(message, "raw-response") {
		t.Fatalf("normalized provider error exposed cause content: %q", message)
	}
}

func TestTranslateNormalizesWrappedTypedProviderErrorWithoutExposingWrapper(t *testing.T) {
	secret := errors.New("credential=secret-token")
	providerError := NewProviderError(ErrorRateLimited, "", 429, true, secret)
	wrapper := fmt.Errorf("raw-response=private-body: %w", providerError)
	provider := &fakeProvider{
		name: "typed",
		translateFn: func(context.Context, Request) (ProviderResult, error) {
			return ProviderResult{}, wrapper
		},
	}
	client, err := New(WithProviders(provider))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	_, err = client.Translate(context.Background(), Request{Text: "Hello", TargetLanguage: "fr", Provider: "typed"})
	var typed *Error
	if !errors.As(err, &typed) {
		t.Fatalf("Translate error type = %T, want *Error", err)
	}
	if typed == providerError {
		t.Fatal("Translate returned the provider's typed error without a privacy boundary")
	}
	if typed.Kind != ErrorRateLimited || typed.Provider != "typed" || typed.StatusCode != 429 || !typed.Retryable {
		t.Fatalf("normalized typed error = %#v", typed)
	}
	if !errors.Is(err, wrapper) || !errors.Is(err, providerError) || !errors.Is(err, secret) {
		t.Fatal("normalized typed error did not retain its wrapped causes")
	}
	if message := err.Error(); strings.Contains(message, "private-body") || strings.Contains(message, "secret-token") || strings.Contains(message, "raw-response") {
		t.Fatalf("normalized typed error exposed cause content: %q", message)
	}
}

func TestTranslateRecordsProviderDuration(t *testing.T) {
	provider := &fakeProvider{
		name: "slow",
		translateFn: func(context.Context, Request) (ProviderResult, error) {
			time.Sleep(20 * time.Millisecond)
			return ProviderResult{Text: "translated"}, nil
		},
	}
	client, err := New(WithProviders(provider))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	result, err := client.Translate(context.Background(), Request{Text: "Hello", TargetLanguage: "fr", Provider: "slow"})
	if err != nil {
		t.Fatalf("Translate returned error: %v", err)
	}
	if result.Duration < 15*time.Millisecond {
		t.Fatalf("result duration = %v, want at least 15ms", result.Duration)
	}
}

func TestClientSupportsConcurrentExplicitCalls(t *testing.T) {
	provider := &fakeProvider{name: "shared"}
	client, err := New(WithProviders(provider))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	const callCount = 64
	var wait sync.WaitGroup
	errorsByCall := make(chan error, callCount)
	for range callCount {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, translateErr := client.Translate(context.Background(), Request{Text: "Hello", TargetLanguage: "fr", Provider: "shared"})
			if translateErr == nil && result.Text != "translated" {
				translateErr = errors.New("unexpected translation result")
			}
			errorsByCall <- translateErr
		}()
	}
	wait.Wait()
	close(errorsByCall)

	for translateErr := range errorsByCall {
		if translateErr != nil {
			t.Fatalf("concurrent Translate returned error: %v", translateErr)
		}
	}
	if provider.calls.Load() != callCount {
		t.Fatalf("provider calls = %d, want %d", provider.calls.Load(), callCount)
	}
}

func assertErrorKind(t *testing.T, err error, want ErrorKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want kind %q", want)
	}
	if !IsKind(err, want) {
		t.Fatalf("error = %v, want kind %q", err, want)
	}
}

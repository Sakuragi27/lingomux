package lingomux

import (
	"context"
	"errors"
	"fmt"
	"reflect"
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
		WithTimeout(10*time.Second),
		WithAttemptTimeout(20*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	_, err = client.Translate(context.Background(), Request{Text: "Hello", TargetLanguage: "fr", Provider: "deadline"})
	if err != nil {
		t.Fatalf("Translate returned error: %v", err)
	}
	if remaining < 8*time.Second || remaining > 10*time.Second {
		t.Fatalf("provider deadline remaining = %v, want approximately 10s", remaining)
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

func TestTranslateAutoUsesSerialRegistrationOrderAndSkipsUnsupported(t *testing.T) {
	var events []string
	var active atomic.Int64
	makeProvider := func(name string, supported, succeeds bool) *fakeProvider {
		return &fakeProvider{name: name, supportsFn: func(source, target string) bool {
			if source != "en-US" || target != "fr" {
				t.Errorf("Supports languages = %q, %q", source, target)
			}
			events = append(events, "supports:"+name)
			return supported
		}, translateFn: func(_ context.Context, request Request) (ProviderResult, error) {
			if active.Add(1) != 1 {
				t.Error("provider attempts overlapped")
			}
			defer active.Add(-1)
			if request.Provider != name || request.Text != "Hello" {
				t.Errorf("attempt request = %#v", request)
			}
			events = append(events, "translate:"+name)
			if succeeds {
				return ProviderResult{Text: "bonjour", SourceLanguage: "EN-us"}, nil
			}
			return ProviderResult{}, NewProviderError(ErrorUnavailable, "", 503, true, nil)
		}}
	}
	client := newTestClient(t, WithProviders(makeProvider("first", true, false), makeProvider("skip", false, false), makeProvider("second", true, true), makeProvider("unused", true, true)))
	result, err := client.Translate(context.Background(), Request{Text: "Hello", SourceLanguage: "EN-us", TargetLanguage: "FR"})
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	want := []string{"supports:first", "supports:skip", "supports:second", "supports:unused", "translate:first", "translate:second"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	if result.Text != "bonjour" || result.Provider != "second" || result.SourceLanguage != "en-US" || result.TargetLanguage != "fr" {
		t.Fatalf("result = %#v", result)
	}
}

func TestTranslateAutoFallsBackOnlyForRetryableErrors(t *testing.T) {
	for _, kind := range []ErrorKind{ErrorRateLimited, ErrorTimeout, ErrorUnavailable, ErrorProviderFailure, ErrorInvalidRequest, ErrorAuthentication, ErrorUnsupportedLanguage, ErrorUnknownProvider} {
		for _, retryable := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/retryable=%t", kind, retryable), func(t *testing.T) {
				cause := errors.New("private provider body")
				first := &fakeProvider{name: "first", translateFn: func(context.Context, Request) (ProviderResult, error) {
					return ProviderResult{}, fmt.Errorf("private wrapper: %w", NewProviderError(kind, "", 503, retryable, cause))
				}}
				second := &fakeProvider{name: "second"}
				client := newTestClient(t, WithProviders(first, second))
				result, err := client.Translate(context.Background(), Request{Text: "Hello", TargetLanguage: "fr"})
				if retryable {
					if err != nil || result.Provider != "second" || second.calls.Load() != 1 {
						t.Fatalf("fallback result=%#v err=%v calls=%d", result, err, second.calls.Load())
					}
				} else {
					assertErrorKind(t, err, kind)
					if _, ok := err.(*Error); !ok || !errors.Is(err, cause) || second.calls.Load() != 0 {
						t.Fatalf("stop error=%v calls=%d", err, second.calls.Load())
					}
				}
				if first.calls.Load() != 1 {
					t.Fatalf("first calls = %d", first.calls.Load())
				}
			})
		}
	}
}

func TestTranslateAutoAggregatesOnlyAttemptedFailuresInOrder(t *testing.T) {
	secret := errors.New("request=private-input credential=private-key raw-body=private-output")
	first := &fakeProvider{name: "first", translateFn: func(context.Context, Request) (ProviderResult, error) { return ProviderResult{}, secret }}
	skip := &fakeProvider{name: "skip", supportsFn: func(string, string) bool { return false }}
	last := &fakeProvider{name: "last", translateFn: func(context.Context, Request) (ProviderResult, error) { return ProviderResult{}, nil }}
	client := newTestClient(t, WithProviders(first, skip, last))
	_, err := client.Translate(context.Background(), Request{Text: "private-input", TargetLanguage: "fr"})
	aggregate, ok := err.(*AggregateError)
	if !ok {
		t.Fatalf("error = %T (%v), want *AggregateError", err, err)
	}
	if len(aggregate.Failures) != 2 || aggregate.Failures[0].Provider != "first" || aggregate.Failures[1].Provider != "last" {
		t.Fatalf("failures = %#v", aggregate.Failures)
	}
	for _, failure := range aggregate.Failures {
		typed, ok := failure.Error.(*Error)
		if !ok || typed.Kind != ErrorProviderFailure || !typed.Retryable || typed.Provider != failure.Provider {
			t.Fatalf("failure = %#v", failure)
		}
	}
	if !errors.Is(err, secret) {
		t.Fatal("aggregate lost provider cause")
	}
	if strings.Contains(err.Error(), "private-") {
		t.Fatalf("aggregate exposed payload: %v", err)
	}
	if skip.calls.Load() != 0 {
		t.Fatal("unsupported provider was attempted")
	}
}

func TestTranslateAutoWithNoEligibleProvider(t *testing.T) {
	provider := &fakeProvider{name: "skip", supportsFn: func(string, string) bool { return false }}
	client := newTestClient(t, WithProviders(provider))
	_, err := client.Translate(context.Background(), Request{Text: "Hello", TargetLanguage: "fr"})
	assertErrorKind(t, err, ErrorUnsupportedLanguage)
	if provider.calls.Load() != 0 {
		t.Fatal("unsupported provider was attempted")
	}
}

func newTestClient(t *testing.T, options ...Option) *Client {
	t.Helper()
	client, err := New(options...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

func TestTranslateAutoAssignsAttemptDeadlines(t *testing.T) {
	for _, tt := range []struct {
		name                                        string
		total, attempt, firstMin, firstMax, lastMin time.Duration
	}{
		{"default cap", 10 * time.Second, 0, 2 * time.Second, 3 * time.Second, 8 * time.Second},
		{"configured cap", 10 * time.Second, 2 * time.Second, time.Second, 2 * time.Second, 8 * time.Second},
		{"total shorter than cap", 2 * time.Second, 3 * time.Second, time.Second, 2 * time.Second, time.Second},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var firstCtx, lastCtx context.Context
			var firstRemaining, lastRemaining time.Duration
			first := &fakeProvider{name: "first", translateFn: func(ctx context.Context, _ Request) (ProviderResult, error) {
				firstCtx = ctx
				deadline, ok := ctx.Deadline()
				if !ok {
					t.Error("first context has no deadline")
				}
				firstRemaining = time.Until(deadline)
				return ProviderResult{}, errors.New("transport failure")
			}}
			last := &fakeProvider{name: "last", translateFn: func(ctx context.Context, _ Request) (ProviderResult, error) {
				if firstCtx.Err() != context.Canceled {
					t.Errorf("first context not canceled before next attempt: %v", firstCtx.Err())
				}
				lastCtx = ctx
				deadline, ok := ctx.Deadline()
				if !ok {
					t.Error("last context has no deadline")
				}
				lastRemaining = time.Until(deadline)
				return ProviderResult{Text: "translated"}, nil
			}}
			skip := &fakeProvider{name: "trailing-skip", supportsFn: func(string, string) bool { return false }}
			options := []Option{WithProviders(first, last, skip), WithTimeout(tt.total)}
			if tt.attempt > 0 {
				options = append(options, WithAttemptTimeout(tt.attempt))
			}
			client := newTestClient(t, options...)
			_, err := client.Translate(context.Background(), Request{Text: "Hello", TargetLanguage: "fr"})
			if err != nil {
				t.Fatalf("Translate: %v", err)
			}
			if firstRemaining < tt.firstMin || firstRemaining > tt.firstMax {
				t.Fatalf("first remaining = %v, want [%v,%v]", firstRemaining, tt.firstMin, tt.firstMax)
			}
			if lastRemaining < tt.lastMin || lastRemaining > tt.total {
				t.Fatalf("last remaining = %v, want [%v,%v]", lastRemaining, tt.lastMin, tt.total)
			}
			if lastCtx.Err() != context.Canceled {
				t.Fatalf("last attempt context not canceled: %v", lastCtx.Err())
			}
		})
	}
}

func TestTranslateAutoStopsWhenContextExpiresDuringEligibility(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var supportCalls int
	first := &fakeProvider{name: "first", supportsFn: func(string, string) bool { cancel(); return true }}
	second := &fakeProvider{name: "second", supportsFn: func(string, string) bool { supportCalls++; return true }}
	client := newTestClient(t, WithProviders(first, second))
	_, err := client.Translate(ctx, Request{Text: "Hello", TargetLanguage: "fr"})
	assertErrorKind(t, err, ErrorTimeout)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("timeout lost cancellation cause")
	}
	if first.calls.Load() != 0 || second.calls.Load() != 0 || supportCalls != 0 {
		t.Fatal("router continued after eligibility cancellation")
	}
}

func TestTranslateAutoStopsWhenTotalDeadlineExpiresDuringAttempt(t *testing.T) {
	first := &fakeProvider{name: "first", translateFn: func(ctx context.Context, _ Request) (ProviderResult, error) {
		<-ctx.Done()
		return ProviderResult{}, ctx.Err()
	}}
	second := &fakeProvider{name: "second"}
	client := newTestClient(t, WithProviders(first, second), WithTimeout(30*time.Millisecond), WithAttemptTimeout(time.Second))
	_, err := client.Translate(context.Background(), Request{Text: "Hello", TargetLanguage: "fr"})
	assertErrorKind(t, err, ErrorTimeout)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("timeout lost deadline cause")
	}
	if first.calls.Load() != 1 || second.calls.Load() != 0 {
		t.Fatalf("calls=(%d,%d), want (1,0)", first.calls.Load(), second.calls.Load())
	}
}

func TestAttemptHookReportsCompletedAttemptsWithoutPayload(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprintf("explicit=%t", explicit), func(t *testing.T) {
			var attempts []Attempt
			var attemptCtx context.Context
			first := &fakeProvider{name: "first", translateFn: func(ctx context.Context, _ Request) (ProviderResult, error) {
				attemptCtx = ctx
				return ProviderResult{}, errors.New("secret-input secret-key secret-body")
			}}
			skip := &fakeProvider{name: "skip", supportsFn: func(string, string) bool { return false }}
			last := &fakeProvider{name: "last", translateFn: func(ctx context.Context, _ Request) (ProviderResult, error) {
				if len(attempts) != 1 {
					t.Error("first hook had not completed before next attempt")
				}
				attemptCtx = ctx
				return ProviderResult{Text: "secret-output"}, nil
			}}
			client := newTestClient(t, WithProviders(first, skip, last), WithAttemptHook(func(attempt Attempt) {
				if attemptCtx.Err() != context.Canceled {
					t.Errorf("attempt context still active during hook: %v", attemptCtx.Err())
				}
				attempts = append(attempts, attempt)
			}))
			request := Request{Text: "secret-input", TargetLanguage: "fr"}
			if explicit {
				request.Provider = "first"
			}
			result, err := client.Translate(context.Background(), request)
			wantCount := 2
			if explicit {
				wantCount = 1
				assertErrorKind(t, err, ErrorProviderFailure)
			} else if err != nil {
				t.Fatalf("Translate: %v", err)
			}
			if len(attempts) != wantCount {
				t.Fatalf("hook calls=%d, want %d", len(attempts), wantCount)
			}
			if attempts[0].Provider != "first" || attempts[0].Success || attempts[0].ErrorKind != ErrorProviderFailure || attempts[0].Duration < 0 {
				t.Fatalf("first attempt=%#v", attempts[0])
			}
			if !explicit && (attempts[1].Provider != "last" || !attempts[1].Success || attempts[1].ErrorKind != "" || attempts[1].Duration != result.Duration) {
				t.Fatalf("last attempt=%#v result=%#v", attempts[1], result)
			}
			if strings.Contains(fmt.Sprintf("%+v", attempts), "secret-") {
				t.Fatal("hook exposed payload")
			}
		})
	}
}

func TestAttemptHookBlocksTranslationUntilItCompletes(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseHook := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseHook()
	client := newTestClient(t, WithProviders(&fakeProvider{name: "provider"}), WithAttemptHook(func(Attempt) { close(entered); <-release }))
	done := make(chan error, 1)
	go func() {
		_, err := client.Translate(context.Background(), Request{Text: "Hello", TargetLanguage: "fr", Provider: "provider"})
		done <- err
	}()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("Translate returned before hook entered: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("hook did not start")
	}
	select {
	case err := <-done:
		t.Fatalf("Translate returned while hook blocked: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	releaseHook()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Translate after hook: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Translate did not finish after hook")
	}
}

func TestAttemptHookPanicDoesNotBreakFallbackOrSuccess(t *testing.T) {
	var count int
	first := &fakeProvider{name: "first", translateFn: func(context.Context, Request) (ProviderResult, error) {
		return ProviderResult{}, errors.New("transport failure")
	}}
	last := &fakeProvider{name: "last"}
	client := newTestClient(t, WithProviders(first, last), WithAttemptHook(func(Attempt) { count++; panic("secret hook body") }))
	result, err := client.Translate(context.Background(), Request{Text: "Hello", TargetLanguage: "fr"})
	if err != nil || result.Provider != "last" || count != 2 {
		t.Fatalf("result=%#v err=%v hook calls=%d", result, err, count)
	}
}

func TestAttemptHookIsNotCalledBeforeAProviderAttempt(t *testing.T) {
	var count int
	client := newTestClient(t, WithProviders(&fakeProvider{name: "skip", supportsFn: func(string, string) bool { return false }}), WithAttemptHook(func(Attempt) { count++ }))
	for _, request := range []Request{{Text: "", TargetLanguage: "fr"}, {Text: "Hello", TargetLanguage: "fr", Provider: "missing"}, {Text: "Hello", TargetLanguage: "fr"}, {Text: "Hello", TargetLanguage: "fr", Provider: "skip"}} {
		_, err := client.Translate(context.Background(), request)
		if err == nil {
			t.Fatal("expected routing or validation error")
		}
	}
	if count != 0 {
		t.Fatalf("hook calls=%d, want 0", count)
	}
}

func TestTranslateAutoFallsBackAfterAttemptDeadline(t *testing.T) {
	var attempts []Attempt
	first := &fakeProvider{name: "first", translateFn: func(ctx context.Context, _ Request) (ProviderResult, error) {
		<-ctx.Done()
		return ProviderResult{}, fmt.Errorf("private body: %w", ctx.Err())
	}}
	last := &fakeProvider{name: "last"}
	client := newTestClient(t, WithProviders(first, last), WithTimeout(5*time.Second), WithAttemptTimeout(20*time.Millisecond), WithAttemptHook(func(attempt Attempt) { attempts = append(attempts, attempt) }))
	result, err := client.Translate(context.Background(), Request{Text: "Hello", TargetLanguage: "fr"})
	if err != nil || result.Provider != "last" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if len(attempts) != 2 || attempts[0].ErrorKind != ErrorTimeout {
		t.Fatalf("attempts=%#v", attempts)
	}
}

func TestTranslateAutoHonorsCallerDeadlineForOnlyEligibleProvider(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	wantDeadline, _ := ctx.Deadline()
	provider := &fakeProvider{name: "only", translateFn: func(ctx context.Context, _ Request) (ProviderResult, error) {
		deadline, ok := ctx.Deadline()
		if !ok || !deadline.Equal(wantDeadline) {
			t.Errorf("deadline=%v, want caller deadline %v", deadline, wantDeadline)
		}
		return ProviderResult{Text: "translated"}, nil
	}}
	skip := &fakeProvider{name: "skip", supportsFn: func(string, string) bool { return false }}
	client := newTestClient(t, WithProviders(provider, skip), WithTimeout(time.Hour), WithAttemptTimeout(time.Second))
	_, err := client.Translate(ctx, Request{Text: "Hello", TargetLanguage: "fr"})
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
}

func TestTranslateAutoStopsWhenHookCancelsTotalContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var hookCalls int
	first := &fakeProvider{name: "first", translateFn: func(context.Context, Request) (ProviderResult, error) {
		return ProviderResult{}, errors.New("transport failure")
	}}
	last := &fakeProvider{name: "last"}
	client := newTestClient(t, WithProviders(first, last), WithAttemptHook(func(Attempt) { hookCalls++; cancel() }))
	_, err := client.Translate(ctx, Request{Text: "Hello", TargetLanguage: "fr"})
	assertErrorKind(t, err, ErrorTimeout)
	if !errors.Is(err, context.Canceled) || last.calls.Load() != 0 || hookCalls != 1 {
		t.Fatalf("error=%v last calls=%d hooks=%d", err, last.calls.Load(), hookCalls)
	}
}

func TestTranslateAutoFinalContextFailureIsAggregated(t *testing.T) {
	first := &fakeProvider{name: "first", translateFn: func(context.Context, Request) (ProviderResult, error) {
		return ProviderResult{}, errors.New("transport failure")
	}}
	last := &fakeProvider{name: "last", translateFn: func(ctx context.Context, _ Request) (ProviderResult, error) {
		<-ctx.Done()
		return ProviderResult{}, ctx.Err()
	}}
	client := newTestClient(t, WithProviders(first, last), WithTimeout(30*time.Millisecond))
	_, err := client.Translate(context.Background(), Request{Text: "Hello", TargetLanguage: "fr"})
	aggregate, ok := err.(*AggregateError)
	if !ok || len(aggregate.Failures) != 2 {
		t.Fatalf("error=%T %v, want two aggregated failures", err, err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("aggregate lost final deadline cause")
	}
	assertErrorKind(t, aggregate.Failures[1].Error, ErrorTimeout)
}

func TestTranslateAutoDoesNotAggregateAttemptsThatNeverStarted(t *testing.T) {
	first := &fakeProvider{name: "first", translateFn: func(context.Context, Request) (ProviderResult, error) {
		return ProviderResult{}, errors.New("first failed")
	}}
	last := &fakeProvider{name: "last", translateFn: func(context.Context, Request) (ProviderResult, error) {
		return ProviderResult{}, errors.New("last failed")
	}}
	client := newTestClient(t, WithProviders(first, last), WithAttemptTimeout(time.Nanosecond))
	_, err := client.Translate(context.Background(), Request{Text: "Hello", TargetLanguage: "fr"})
	aggregate, ok := err.(*AggregateError)
	if !ok {
		t.Fatalf("error=%T %v, want aggregate", err, err)
	}
	if got, want := len(aggregate.Failures), int(first.calls.Load()+last.calls.Load()); got != want {
		t.Fatalf("aggregate contains %d failures for %d actual provider calls", got, want)
	}
}

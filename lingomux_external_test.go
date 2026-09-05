package lingomux_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Sakuragi27/lingomux"
)

type externalProvider struct {
	name      string
	supported bool
	result    lingomux.ProviderResult
	err       error
	calls     int
}

func (provider *externalProvider) Name() string {
	return provider.name
}

func (provider *externalProvider) Supports(sourceLanguage, targetLanguage string) bool {
	return provider.supported && sourceLanguage == lingomux.AutoLanguage && targetLanguage == "fr"
}

func (provider *externalProvider) Translate(_ context.Context, _ lingomux.Request) (lingomux.ProviderResult, error) {
	provider.calls++
	return provider.result, provider.err
}

func TestExternalProviderCanBeSelectedExplicitlyAndAutomatically(t *testing.T) {
	explicit := &externalProvider{
		name:      "external",
		supported: true,
		result:    lingomux.ProviderResult{Text: "bonjour", SourceLanguage: "en"},
	}
	client, err := lingomux.New(lingomux.WithProviders(explicit))
	if err != nil {
		t.Fatalf("lingomux.New returned error: %v", err)
	}

	result, err := client.Translate(context.Background(), lingomux.Request{
		Text:           "hello",
		SourceLanguage: lingomux.AutoLanguage,
		TargetLanguage: "fr",
		Provider:       "external",
	})
	if err != nil {
		t.Fatalf("explicit Translate returned error: %v", err)
	}
	if explicit.calls != 1 || result.Text != "bonjour" || result.Provider != "external" {
		t.Fatalf("explicit result = %#v after %d calls", result, explicit.calls)
	}

	first := &externalProvider{
		name:      "first",
		supported: true,
		err: lingomux.NewProviderError(
			lingomux.ErrorUnavailable, "first", 503, true, errors.New("temporary outage"),
		),
	}
	second := &externalProvider{
		name:      "second",
		supported: true,
		result:    lingomux.ProviderResult{Text: "salut", SourceLanguage: "en"},
	}
	autoClient, err := lingomux.New(lingomux.WithProviders(first, second))
	if err != nil {
		t.Fatalf("lingomux.New returned error: %v", err)
	}

	result, err = autoClient.Translate(context.Background(), lingomux.Request{
		Text:           "hello",
		SourceLanguage: lingomux.AutoLanguage,
		TargetLanguage: "fr",
	})
	if err != nil {
		t.Fatalf("automatic Translate returned error: %v", err)
	}
	if first.calls != 1 || second.calls != 1 || result.Text != "salut" || result.Provider != "second" {
		t.Fatalf("automatic result = %#v after calls (%d, %d)", result, first.calls, second.calls)
	}
}

func TestExternalProviderErrorsArePubliclyInspectable(t *testing.T) {
	firstCause := errors.New("first unavailable")
	secondCause := errors.New("second unavailable")
	first := &externalProvider{
		name:      "first",
		supported: true,
		err:       lingomux.NewProviderError(lingomux.ErrorUnavailable, "first", 503, true, firstCause),
	}
	second := &externalProvider{
		name:      "second",
		supported: true,
		err:       lingomux.NewProviderError(lingomux.ErrorRateLimited, "second", 429, true, secondCause),
	}
	client, err := lingomux.New(lingomux.WithProviders(first, second))
	if err != nil {
		t.Fatalf("lingomux.New returned error: %v", err)
	}

	_, err = client.Translate(context.Background(), lingomux.Request{
		Text:           "hello",
		SourceLanguage: lingomux.AutoLanguage,
		TargetLanguage: "fr",
	})
	if err == nil {
		t.Fatal("automatic Translate returned nil error")
	}
	if !lingomux.IsKind(err, lingomux.ErrorUnavailable) || !lingomux.IsKind(err, lingomux.ErrorRateLimited) {
		t.Fatalf("IsKind could not inspect aggregate: %v", err)
	}

	var aggregate *lingomux.AggregateError
	if !errors.As(err, &aggregate) {
		t.Fatalf("error type = %T, want *lingomux.AggregateError", err)
	}
	if len(aggregate.Failures) != 2 || aggregate.Failures[0].Provider != "first" || aggregate.Failures[1].Provider != "second" {
		t.Fatalf("aggregate failures = %#v", aggregate.Failures)
	}
	var typed *lingomux.Error
	if !errors.As(aggregate.Failures[0].Error, &typed) {
		t.Fatalf("first failure type = %T, want *lingomux.Error", aggregate.Failures[0].Error)
	}
	if typed.Kind != lingomux.ErrorUnavailable || typed.StatusCode != 503 || !typed.Retryable {
		t.Fatalf("first typed failure = %#v", typed)
	}
	if !errors.Is(err, firstCause) || !errors.Is(err, secondCause) {
		t.Fatal("aggregate did not preserve provider causes")
	}
}

var _ lingomux.Provider = (*externalProvider)(nil)

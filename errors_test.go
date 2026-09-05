package lingomux

import (
	"errors"
	"strings"
	"testing"
)

func TestProviderErrorPreservesCauseForInspectionWithoutExposingIt(t *testing.T) {
	secret := errors.New("authorization: Bearer secret-token and request body")
	err := NewProviderError(ErrorAuthentication, "google", 401, false, secret)

	if !errors.Is(err, secret) {
		t.Fatal("errors.Is did not find the provider error cause")
	}
	var typed *Error
	if !errors.As(err, &typed) || typed != err {
		t.Fatalf("errors.As did not recover the original typed error: %v", typed)
	}
	if !IsKind(err, ErrorAuthentication) {
		t.Fatalf("IsKind returned false for %q", ErrorAuthentication)
	}
	if message := err.Error(); strings.Contains(message, "secret-token") || strings.Contains(message, "request body") {
		t.Fatalf("Error() exposed cause content: %q", message)
	}
}

func TestAggregateErrorPreservesOrderedFailuresAndDoesNotExposeCauses(t *testing.T) {
	firstSecret := errors.New("api-key=first-secret")
	secondSecret := errors.New("response body=second-secret")
	first := NewProviderError(ErrorTimeout, "google", 504, true, firstSecret)
	second := NewProviderError(ErrorRateLimited, "deepl", 429, true, secondSecret)
	aggregate := &AggregateError{Failures: []ProviderFailure{
		{Provider: "google", Error: first},
		{Provider: "deepl", Error: second},
	}}

	if len(aggregate.Failures) != 2 || aggregate.Failures[0].Provider != "google" || aggregate.Failures[1].Provider != "deepl" {
		t.Fatalf("failure order = %#v, want google then deepl", aggregate.Failures)
	}
	if !errors.Is(aggregate, firstSecret) || !errors.Is(aggregate, secondSecret) {
		t.Fatal("aggregate did not preserve failure causes for errors.Is")
	}
	if !IsKind(aggregate, ErrorRateLimited) {
		t.Fatalf("IsKind returned false for aggregated %q error", ErrorRateLimited)
	}
	if message := aggregate.Error(); strings.Contains(message, "first-secret") || strings.Contains(message, "second-secret") || strings.Contains(message, "response body") {
		t.Fatalf("AggregateError.Error() exposed cause content: %q", message)
	}
}

package lingomux

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestNormalizeProviderContextErrorsPreservesCauseWithoutPayload(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(cause.Error(), func(t *testing.T) {
			wrapped := fmt.Errorf("request=private-input credential=private-key raw-body=private-body: %w", cause)
			err := normalizeProviderError(wrapped, "provider")
			if err.Kind != ErrorTimeout || err.Provider != "provider" || !err.Retryable {
				t.Fatalf("normalized error=%#v", err)
			}
			if !errors.Is(err, wrapped) || !errors.Is(err, cause) {
				t.Fatal("normalization lost context cause")
			}
			if strings.Contains(err.Error(), "private-") {
				t.Fatalf("error exposed payload: %v", err)
			}
		})
	}
}

func TestNormalizeContextCauseTakesPrecedenceOverContradictoryTypedError(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(cause.Error(), func(t *testing.T) {
			providerError := NewProviderError(ErrorAuthentication, "provider", 401, false, cause)
			wrapped := fmt.Errorf("private response: %w", providerError)
			err := normalizeProviderError(wrapped, "provider")
			if err.Kind != ErrorTimeout || !err.Retryable {
				t.Fatalf("context normalization=%#v", err)
			}
			if !errors.Is(err, cause) || !errors.Is(err, providerError) || !errors.Is(err, wrapped) {
				t.Fatal("lost context or typed cause")
			}
			if strings.Contains(err.Error(), "private response") {
				t.Fatalf("exposed wrapper: %v", err)
			}
		})
	}
}

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

func TestErrorHelpersHandleTypedNilErrors(t *testing.T) {
	var typedNil *Error
	if IsKind(typedNil, ErrorTimeout) {
		t.Fatal("IsKind reported a kind for a typed-nil error")
	}

	aggregate := &AggregateError{Failures: []ProviderFailure{{Provider: "google", Error: typedNil}}}
	if got, want := aggregate.Error(), "lingomux: all providers failed: google (provider_failure)"; got != want {
		t.Fatalf("AggregateError.Error() = %q, want %q", got, want)
	}
}

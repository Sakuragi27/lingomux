package lingomux

import (
	"errors"
	"strconv"
	"strings"
)

// ErrorKind classifies errors returned by LingoMux and its providers.
type ErrorKind string

const (
	// ErrorInvalidRequest indicates invalid request data or configuration.
	ErrorInvalidRequest ErrorKind = "invalid_request"
	// ErrorUnknownProvider indicates that the requested provider is not registered.
	ErrorUnknownProvider ErrorKind = "unknown_provider"
	// ErrorUnsupportedLanguage indicates an unsupported source or target language.
	ErrorUnsupportedLanguage ErrorKind = "unsupported_language"
	// ErrorAuthentication indicates rejected provider credentials or permissions.
	ErrorAuthentication ErrorKind = "authentication"
	// ErrorRateLimited indicates that a provider throttled the request.
	ErrorRateLimited ErrorKind = "rate_limited"
	// ErrorTimeout indicates cancellation or an exceeded deadline.
	ErrorTimeout ErrorKind = "timeout"
	// ErrorUnavailable indicates an unavailable provider or transport.
	ErrorUnavailable ErrorKind = "unavailable"
	// ErrorProviderFailure indicates a provider failure without a more specific kind.
	ErrorProviderFailure ErrorKind = "provider_failure"
)

// Error is a typed, privacy-preserving error from the router or a provider.
type Error struct {
	Kind       ErrorKind
	Provider   string
	StatusCode int
	Retryable  bool
	Cause      error
}

// Error returns a summary that omits the underlying cause and payload data.
func (err *Error) Error() string {
	if err == nil {
		return "<nil>"
	}

	parts := []string{"lingomux", string(err.Kind)}
	if err.Provider != "" {
		parts = append(parts, "provider="+err.Provider)
	}
	if err.StatusCode != 0 {
		parts = append(parts, "status="+strconv.Itoa(err.StatusCode))
	}
	return strings.Join(parts, ": ")
}

// Unwrap exposes the underlying cause for errors.Is and errors.As.
func (err *Error) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}

// NewProviderError constructs a typed provider error for a custom adapter.
func NewProviderError(kind ErrorKind, provider string, statusCode int, retryable bool, cause error) *Error {
	return &Error{
		Kind:       kind,
		Provider:   provider,
		StatusCode: statusCode,
		Retryable:  retryable,
		Cause:      cause,
	}
}

// IsKind reports whether err or one of its wrapped errors has kind.
func IsKind(err error, kind ErrorKind) bool {
	if err == nil {
		return false
	}
	if typed, ok := err.(*Error); ok && typed != nil && typed.Kind == kind {
		return true
	}
	if multiple, ok := err.(interface{ Unwrap() []error }); ok {
		for _, nested := range multiple.Unwrap() {
			if IsKind(nested, kind) {
				return true
			}
		}
		return false
	}
	if single, ok := err.(interface{ Unwrap() error }); ok {
		return IsKind(single.Unwrap(), kind)
	}
	return false
}

// ProviderFailure records one provider error in an automatic routing attempt.
type ProviderFailure struct {
	Provider string
	Error    error
}

// AggregateError records the ordered failures from automatic routing.
type AggregateError struct {
	Failures []ProviderFailure
}

// Error summarizes failures in attempt order without exposing their causes.
func (err *AggregateError) Error() string {
	if err == nil || len(err.Failures) == 0 {
		return "lingomux: all providers failed"
	}

	parts := make([]string, 0, len(err.Failures))
	for _, failure := range err.Failures {
		parts = append(parts, safeFailureSummary(failure))
	}
	return "lingomux: all providers failed: " + strings.Join(parts, ", ")
}

func safeFailureSummary(failure ProviderFailure) string {
	provider := failure.Provider
	if typed, ok := failure.Error.(*Error); ok && typed != nil {
		if provider == "" {
			provider = typed.Provider
		}
		parts := []string{string(typed.Kind)}
		if typed.StatusCode != 0 {
			parts = append(parts, "status="+strconv.Itoa(typed.StatusCode))
		}
		if provider != "" {
			return provider + " (" + strings.Join(parts, ", ") + ")"
		}
		return strings.Join(parts, ", ")
	}
	if provider != "" {
		return provider + " (" + string(ErrorProviderFailure) + ")"
	}
	return string(ErrorProviderFailure)
}

// Unwrap exposes all provider failures for errors.Is and errors.As.
func (err *AggregateError) Unwrap() []error {
	if err == nil {
		return nil
	}
	causes := make([]error, 0, len(err.Failures))
	for _, failure := range err.Failures {
		if failure.Error != nil {
			causes = append(causes, failure.Error)
		}
	}
	return causes
}

var _ error = (*Error)(nil)
var _ error = (*AggregateError)(nil)
var _ = errors.Is

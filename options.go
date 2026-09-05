package lingomux

import "time"

const (
	defaultTimeout        = 15 * time.Second
	defaultAttemptTimeout = 3 * time.Second
)

type clientConfig struct {
	providers      []Provider
	timeout        time.Duration
	attemptTimeout time.Duration
	maxTextRunes   int
	attemptHook    AttemptHook
}

// Option configures a Client during construction.
type Option func(*clientConfig) error

// WithProviders registers providers in the order used for automatic routing.
func WithProviders(providers ...Provider) Option {
	copied := append([]Provider(nil), providers...)
	return func(config *clientConfig) error {
		config.providers = append([]Provider(nil), copied...)
		return nil
	}
}

// WithTimeout sets the total timeout for one translation call.
func WithTimeout(timeout time.Duration) Option {
	return func(config *clientConfig) error {
		if timeout <= 0 {
			return invalidRequestError()
		}
		config.timeout = timeout
		return nil
	}
}

// WithAttemptTimeout sets the timeout for a non-final automatic routing attempt.
func WithAttemptTimeout(timeout time.Duration) Option {
	return func(config *clientConfig) error {
		if timeout <= 0 {
			return invalidRequestError()
		}
		config.attemptTimeout = timeout
		return nil
	}
}

// WithMaxTextRunes sets the maximum number of Unicode code points in a request.
func WithMaxTextRunes(limit int) Option {
	return func(config *clientConfig) error {
		if limit <= 0 {
			return invalidRequestError()
		}
		config.maxTextRunes = limit
		return nil
	}
}

// WithAttemptHook installs a synchronous callback for completed provider attempts.
func WithAttemptHook(hook AttemptHook) Option {
	return func(config *clientConfig) error {
		config.attemptHook = hook
		return nil
	}
}

package lingomux

import "context"

// Provider translates requests for one vendor or custom backend.
type Provider interface {
	Name() string
	Supports(sourceLanguage, targetLanguage string) bool
	Translate(context.Context, Request) (ProviderResult, error)
}

package lingomux

import "time"

// Result is the normalized result returned by a successful translation.
type Result struct {
	Text           string
	SourceLanguage string
	TargetLanguage string
	Provider       string
	Duration       time.Duration
}

// ProviderResult is a provider's untranslated routing metadata and translated text.
type ProviderResult struct {
	Text           string
	SourceLanguage string
}

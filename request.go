package lingomux

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// AutoLanguage requests source-language detection from a provider that supports it.
	AutoLanguage = "auto"
	// AutoProvider requests serial selection from the client's configured providers.
	AutoProvider = "auto"
	// UndeterminedLanguage denotes a provider result with no detected source language.
	UndeterminedLanguage = "und"
	// DefaultMaxTextRunes is the maximum number of Unicode code points in one request.
	DefaultMaxTextRunes = 5000
)

// Request is a single text segment to translate.
type Request struct {
	Text           string
	SourceLanguage string
	TargetLanguage string
	Provider       string
}

func normalizeRequest(request Request, maxTextRunes int) (Request, error) {
	if !utf8.ValidString(request.Text) {
		return Request{}, invalidRequestError()
	}
	if !hasNonWhitespace(request.Text) {
		return Request{}, invalidRequestError()
	}
	if maxTextRunes <= 0 || utf8.RuneCountInString(request.Text) > maxTextRunes {
		return Request{}, invalidRequestError()
	}

	var err error
	if request.SourceLanguage == "" {
		request.SourceLanguage = AutoLanguage
	}
	request.SourceLanguage, err = normalizeLanguage(request.SourceLanguage, true)
	if err != nil {
		return Request{}, invalidRequestError()
	}
	if request.TargetLanguage == "" {
		return Request{}, invalidRequestError()
	}
	request.TargetLanguage, err = normalizeLanguage(request.TargetLanguage, false)
	if err != nil {
		return Request{}, invalidRequestError()
	}

	if request.Provider == "" {
		request.Provider = AutoProvider
	}
	request.Provider, err = normalizeProviderName(request.Provider)
	if err != nil {
		return Request{}, invalidRequestError()
	}
	return request, nil
}

func hasNonWhitespace(value string) bool {
	return strings.IndexFunc(value, func(r rune) bool { return !unicode.IsSpace(r) }) >= 0
}

func normalizeProviderName(value string) (string, error) {
	if value == "" {
		return "", invalidRequestError()
	}

	var normalized strings.Builder
	normalized.Grow(len(value))
	for index, r := range value {
		if r > unicode.MaxASCII {
			return "", invalidRequestError()
		}
		if index == 0 {
			if !isASCIILetter(r) {
				return "", invalidRequestError()
			}
		} else if !isASCIILetter(r) && !isASCIIDigit(r) && r != '_' && r != '-' {
			return "", invalidRequestError()
		}
		normalized.WriteRune(toASCIILower(r))
	}
	return normalized.String(), nil
}

func invalidRequestError() *Error {
	return &Error{Kind: ErrorInvalidRequest}
}

func isASCIILetter(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
}

func isASCIIDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

func toASCIILower(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + ('a' - 'A')
	}
	return r
}

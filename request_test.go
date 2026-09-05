package lingomux

import (
	"strings"
	"testing"
)

func TestNormalizeRequestRejectsWhitespaceTextWithoutMutatingIt(t *testing.T) {
	request := Request{Text: " \t\n", TargetLanguage: "en"}

	if _, err := normalizeRequest(request, DefaultMaxTextRunes); err == nil {
		t.Fatal("normalizeRequest accepted whitespace-only text")
	}
	if request.Text != " \t\n" {
		t.Fatalf("request text was mutated to %q", request.Text)
	}
}

func TestNormalizeRequestRejectsInvalidUTF8WithoutMutatingIt(t *testing.T) {
	request := Request{Text: "valid\xfftext", TargetLanguage: "en"}

	if _, err := normalizeRequest(request, DefaultMaxTextRunes); err == nil {
		t.Fatal("normalizeRequest accepted invalid UTF-8 text")
	}
	if request.Text != "valid\xfftext" {
		t.Fatalf("request text was mutated to %q", request.Text)
	}
}

func TestNormalizeRequestNormalizesDefaultsAndProviderName(t *testing.T) {
	request := Request{
		Text:           "Hello",
		TargetLanguage: "ZH-cn",
		Provider:       "Google_Cloud",
	}

	got, err := normalizeRequest(request, DefaultMaxTextRunes)
	if err != nil {
		t.Fatalf("normalizeRequest returned error: %v", err)
	}
	if got.Text != request.Text {
		t.Fatalf("normalized text = %q, want original %q", got.Text, request.Text)
	}
	if got.SourceLanguage != AutoLanguage {
		t.Fatalf("source language = %q, want %q", got.SourceLanguage, AutoLanguage)
	}
	if got.TargetLanguage != "zh-CN" {
		t.Fatalf("target language = %q, want zh-CN", got.TargetLanguage)
	}
	if got.Provider != "google_cloud" {
		t.Fatalf("provider = %q, want google_cloud", got.Provider)
	}
}

func TestNormalizeRequestRejectsMissingOrAutomaticTarget(t *testing.T) {
	for _, target := range []string{"", AutoLanguage} {
		t.Run(target, func(t *testing.T) {
			_, err := normalizeRequest(Request{Text: "Hello", TargetLanguage: target}, DefaultMaxTextRunes)
			if err == nil {
				t.Fatalf("normalizeRequest accepted target %q", target)
			}
			if !IsKind(err, ErrorInvalidRequest) {
				t.Fatalf("normalizeRequest error kind = %v, want %q", err, ErrorInvalidRequest)
			}
		})
	}
}

func TestNormalizeRequestDefaultsEmptyProviderToAuto(t *testing.T) {
	got, err := normalizeRequest(Request{Text: "Hello", TargetLanguage: "en"}, DefaultMaxTextRunes)
	if err != nil {
		t.Fatalf("normalizeRequest returned error: %v", err)
	}
	if got.Provider != AutoProvider {
		t.Fatalf("provider = %q, want %q", got.Provider, AutoProvider)
	}
}

func TestNormalizeRequestRejectsInvalidProviderIdentifiers(t *testing.T) {
	for _, provider := range []string{"gøøgle", "google cloud", "google!", "1google", "-google"} {
		t.Run(provider, func(t *testing.T) {
			_, err := normalizeRequest(Request{Text: "Hello", TargetLanguage: "en", Provider: provider}, DefaultMaxTextRunes)
			if err == nil {
				t.Fatalf("normalizeRequest accepted provider %q", provider)
			}
			if !IsKind(err, ErrorInvalidRequest) {
				t.Fatalf("provider %q error kind = %v, want %q", provider, err, ErrorInvalidRequest)
			}
		})
	}
}

func TestNormalizeRequestCountsUnicodeCodePoints(t *testing.T) {
	valid := Request{Text: strings.Repeat("界", DefaultMaxTextRunes), TargetLanguage: "en"}
	if _, err := normalizeRequest(valid, DefaultMaxTextRunes); err != nil {
		t.Fatalf("normalizeRequest rejected %d code points: %v", DefaultMaxTextRunes, err)
	}

	invalid := Request{Text: strings.Repeat("界", DefaultMaxTextRunes+1), TargetLanguage: "en"}
	if _, err := normalizeRequest(invalid, DefaultMaxTextRunes); err == nil {
		t.Fatalf("normalizeRequest accepted %d code points", DefaultMaxTextRunes+1)
	}
}

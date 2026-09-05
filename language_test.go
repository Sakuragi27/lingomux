package lingomux

import "testing"

func TestNormalizeLanguageCanonicalizesValidTags(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "lowercase language", input: "en", want: "en"},
		{name: "uppercase language", input: "JA", want: "ja"},
		{name: "mixed case region", input: "zh-cn", want: "zh-CN"},
		{name: "uppercase region", input: "zh-CN", want: "zh-CN"},
		{name: "mixed language and region", input: "ZH-cn", want: "zh-CN"},
		{name: "traditional Chinese", input: "zh-tw", want: "zh-TW"},
		{name: "Brazilian Portuguese", input: "pt-br", want: "pt-BR"},
		{name: "script and region", input: "zh-hant-tw", want: "zh-Hant-TW"},
		{name: "numeric region", input: "es-419", want: "es-419"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeLanguage(tt.input, false)
			if err != nil {
				t.Fatalf("normalizeLanguage(%q) returned error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("normalizeLanguage(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNormalizeLanguageOnlyAllowsAutoForSourceDetection(t *testing.T) {
	got, err := normalizeLanguage(AutoLanguage, true)
	if err != nil || got != AutoLanguage {
		t.Fatalf("normalizeLanguage(auto, true) = (%q, %v), want (auto, nil)", got, err)
	}

	if _, err := normalizeLanguage(AutoLanguage, false); err == nil {
		t.Fatal("normalizeLanguage(auto, false) succeeded, want error")
	}
}

func TestNormalizeLanguageRejectsMalformedTags(t *testing.T) {
	for _, input := range []string{"en_US", "a", "zh--CN", "auto-US", " en", "en ", "en US"} {
		t.Run(input, func(t *testing.T) {
			if _, err := normalizeLanguage(input, true); err == nil {
				t.Fatalf("normalizeLanguage(%q, true) succeeded, want error", input)
			}
		})
	}
}

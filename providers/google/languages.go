package google

// canonicalToGoogle is deliberately explicit. Google Basic v2 uses "pt" for
// Portuguese, so pt-BR support is an intentional many-to-one mapping rather
// than inferred region stripping.
var canonicalToGoogle = map[string]string{
	"ar":    "ar",
	"de":    "de",
	"en":    "en",
	"es":    "es",
	"fr":    "fr",
	"hi":    "hi",
	"it":    "it",
	"ja":    "ja",
	"ko":    "ko",
	"pt":    "pt",
	"pt-BR": "pt",
	"ru":    "ru",
	"zh-CN": "zh-CN",
	"zh-TW": "zh-TW",
}

var googleToCanonical = map[string]string{
	"ar":    "ar",
	"de":    "de",
	"en":    "en",
	"es":    "es",
	"fr":    "fr",
	"hi":    "hi",
	"it":    "it",
	"ja":    "ja",
	"ko":    "ko",
	"pt":    "pt",
	"ru":    "ru",
	"zh-CN": "zh-CN",
	"zh-TW": "zh-TW",
}

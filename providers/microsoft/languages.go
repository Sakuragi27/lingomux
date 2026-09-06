package microsoft

// canonicalToMicrosoft is deliberately explicit. Azure uses script tags for
// Chinese and distinct documented codes for Brazilian and European
// Portuguese; no region is inferred or stripped.
var canonicalToMicrosoft = map[string]string{
	"ar":    "ar",
	"de":    "de",
	"en":    "en",
	"es":    "es",
	"fr":    "fr",
	"hi":    "hi",
	"id":    "id",
	"it":    "it",
	"ja":    "ja",
	"ko":    "ko",
	"pt":    "pt",
	"pt-BR": "pt",
	"pt-PT": "pt-pt",
	"ru":    "ru",
	"th":    "th",
	"vi":    "vi",
	"zh-CN": "zh-Hans",
	"zh-TW": "zh-Hant",
}

var microsoftToCanonical = map[string]string{
	"ar":      "ar",
	"de":      "de",
	"en":      "en",
	"es":      "es",
	"fr":      "fr",
	"hi":      "hi",
	"id":      "id",
	"it":      "it",
	"ja":      "ja",
	"ko":      "ko",
	"pt":      "pt-BR",
	"pt-pt":   "pt-PT",
	"ru":      "ru",
	"th":      "th",
	"vi":      "vi",
	"zh-Hans": "zh-CN",
	"zh-Hant": "zh-TW",
}

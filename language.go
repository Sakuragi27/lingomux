package lingomux

import "strings"

// normalizeLanguage validates and canonicalizes the BCP 47-style subset used
// by this release. It intentionally does not decide whether a vendor supports
// the resulting language tag.
func normalizeLanguage(value string, allowAuto bool) (string, error) {
	if strings.EqualFold(value, AutoLanguage) {
		if allowAuto {
			return AutoLanguage, nil
		}
		return "", invalidRequestError()
	}

	parts := strings.Split(value, "-")
	if len(parts) < 1 || len(parts) > 3 || !isLanguagePart(parts[0]) {
		return "", invalidRequestError()
	}

	canonical := []string{strings.ToLower(parts[0])}
	if len(parts) == 1 {
		return canonical[0], nil
	}

	second := parts[1]
	if isScriptPart(second) {
		canonical = append(canonical, strings.ToUpper(second[:1])+strings.ToLower(second[1:]))
		if len(parts) == 2 {
			return strings.Join(canonical, "-"), nil
		}
		if !isRegionPart(parts[2]) {
			return "", invalidRequestError()
		}
		canonical = append(canonical, canonicalRegion(parts[2]))
		return strings.Join(canonical, "-"), nil
	}

	if len(parts) != 2 || !isRegionPart(second) {
		return "", invalidRequestError()
	}
	canonical = append(canonical, canonicalRegion(second))
	return strings.Join(canonical, "-"), nil
}

func isLanguagePart(value string) bool {
	if len(value) != 2 && len(value) != 3 {
		return false
	}
	for _, r := range value {
		if !isASCIILetter(r) {
			return false
		}
	}
	return true
}

func isScriptPart(value string) bool {
	if len(value) != 4 {
		return false
	}
	for _, r := range value {
		if !isASCIILetter(r) {
			return false
		}
	}
	return true
}

func isRegionPart(value string) bool {
	if len(value) == 2 {
		for _, r := range value {
			if !isASCIILetter(r) {
				return false
			}
		}
		return true
	}
	if len(value) == 3 {
		for _, r := range value {
			if !isASCIIDigit(r) {
				return false
			}
		}
		return true
	}
	return false
}

func canonicalRegion(value string) string {
	if len(value) == 2 {
		return strings.ToUpper(value)
	}
	return value
}

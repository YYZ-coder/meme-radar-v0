package metadata

import (
	"encoding/json"
	"strconv"
	"unicode/utf8"
)

// keepRaw runs after JSON decoding. Inspect escapes before Go replaces isolated
// UTF-16 surrogates, so PostgreSQL's stricter jsonb representation cannot reject
// an otherwise usable display document. Escaped backslashes are not Unicode.
func keepRaw(doc *Document, data []byte) {
	doc.Raw = nil
	doc.RawOmitted = jsonbOmission(data)
	if doc.RawOmitted == "" && len(data) <= 64<<10 {
		doc.Raw = append(json.RawMessage(nil), data...)
	}
}

func jsonbOmission(data []byte) string {
	if !utf8.Valid(data) {
		return "invalid UTF-8"
	}
	for i := 0; i < len(data); i++ {
		if data[i] != '\\' {
			continue
		}
		i++
		if i >= len(data) || data[i] != 'u' || i+4 >= len(data) {
			continue
		}
		code, _ := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
		i += 4
		if code == 0 {
			return "NUL Unicode escape"
		}
		if code >= 0xdc00 && code <= 0xdfff {
			return "isolated UTF-16 surrogate"
		}
		if code >= 0xd800 && code <= 0xdbff {
			if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
				return "isolated UTF-16 surrogate"
			}
			low, _ := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
			if low < 0xdc00 || low > 0xdfff {
				return "isolated UTF-16 surrogate"
			}
			i += 6
		}
	}
	return ""
}

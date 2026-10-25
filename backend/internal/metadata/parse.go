package metadata

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// ParseMetadata decodes a JSON object and normalizes its display fields.
func ParseMetadata(data []byte, gateway string) (Document, error) {
	if len(data) > 512<<10 {
		return Document{}, fmt.Errorf("metadata: response exceeds 512 KiB")
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return Document{}, fmt.Errorf("metadata: JSON: %w", err)
	}
	if obj == nil {
		return Document{}, fmt.Errorf("metadata: expected JSON object")
	}
	doc := Document{Socials: map[string]string{}, Name: cleanName(text(obj, "name"), 128), Symbol: cleanName(text(obj, "symbol"), 32), Description: clip(cleanText(text(obj, "description")), 1000)}
	image := text(obj, "image")
	if image == "" {
		image = text(obj, "image_url")
	}
	doc.ImageURL = displayURL(image, gateway)
	// Flat fields take priority; nested sources fill absent fields.
	mergeSocials(doc.Socials, obj)
	var ext, links map[string]json.RawMessage
	_ = json.Unmarshal(obj["extensions"], &ext)
	mergeSocials(doc.Socials, ext)
	if json.Unmarshal(obj["links"], &links) == nil {
		mergeSocials(doc.Socials, links)
	} else {
		var entries []map[string]json.RawMessage
		if json.Unmarshal(obj["links"], &entries) == nil {
			for _, entry := range entries {
				key := text(entry, "type")
				if key == "" {
					key = text(entry, "name")
				}
				mergeSocials(doc.Socials, map[string]json.RawMessage{key: entry["url"]})
			}
		}
	}
	keepRaw(&doc, data)
	return doc, nil
}

func text(obj map[string]json.RawMessage, key string) string {
	var value string
	_ = json.Unmarshal(obj[key], &value)
	return value
}

func cleanName(s string, n int) string { return clip(strings.TrimSpace(cleanText(s)), n) }

func cleanText(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(s, "\ufffd"))
}

func clip(s string, n int) string {
	chars := []rune(s)
	if len(chars) > n {
		chars = chars[:n]
	}
	return string(chars)
}

func displayURL(s, gateway string) string {
	normalized, err := NormalizeURI(cleanText(s), gateway)
	if err != nil {
		return ""
	}
	// Validate after clipping too: a cut percent escape must not become a URL.
	normalized = clip(normalized, 256)
	if _, err := NormalizeURI(normalized, gateway); err != nil {
		return ""
	}
	return normalized
}

var username = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

func mergeSocials(out map[string]string, obj map[string]json.RawMessage) {
	for _, key := range []string{"twitter", "x", "telegram", "website", "github"} {
		canonical := key
		if key == "x" {
			canonical = "twitter"
		}
		if out[canonical] != "" {
			continue
		}
		value := strings.TrimSpace(cleanText(text(obj, key)))
		if (canonical == "twitter" || canonical == "telegram") && username.MatchString(strings.TrimPrefix(value, "@")) {
			base := "https://x.com/"
			if canonical == "telegram" {
				base = "https://t.me/"
			}
			value = base + strings.TrimPrefix(value, "@")
		}
		if !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
			continue
		}
		if normalized := displayURL(value, ""); normalized != "" {
			out[canonical] = normalized
		}
	}
}

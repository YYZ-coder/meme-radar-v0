// Package smlist parses and validates uploaded smart-money list files (product/08-admin §1).
package smlist

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/meme-radar/radar/internal/api"
)

// Limits for one uploaded file.
const (
	MaxBytes = 5 << 20
	MaxRows  = 10_000
)

var (
	secretHeader = regexp.MustCompile(`(?i)(private|secret|mnemonic|seed|助记词|私钥)`)
	secretValues = []*regexp.Regexp{
		regexp.MustCompile(`^(0x)?[0-9a-fA-F]{64}$`),                          // EVM private key
		regexp.MustCompile(`^[1-9A-HJ-NP-Za-km-z]{86,90}$`),                   // Solana secret key
		regexp.MustCompile(`^\[(\s*\d{1,3}\s*,){63}\s*\d{1,3}\s*\]$`),         // Solana keypair array
		regexp.MustCompile(`^([a-z]+\s+){11}[a-z]+$|^([a-z]+\s+){23}[a-z]+$`), // BIP-39 phrase
	}
	solanaAddr = regexp.MustCompile(`^[1-9A-HJ-NP-Za-km-z]{32,44}$`)
	evmAddr    = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
	twitter    = regexp.MustCompile(`^(@?[A-Za-z0-9_]{1,15}|https://(x|twitter)\.com/[A-Za-z0-9_]{1,15})$`)
)

func fileError(status int, code, message string) *api.Error {
	return api.Errorf(status, code, "file", message)
}

type record map[string]any

func readRecords(fileName string, content []byte) ([]record, error) {
	trimmed := bytes.TrimSpace(content)
	if strings.HasSuffix(strings.ToLower(fileName), ".json") || bytes.HasPrefix(trimmed, []byte("[")) {
		var items []any
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return nil, fileError(http.StatusBadRequest, "invalid_file", "JSON must be an array of objects")
		}
		out := make([]record, len(items))
		for i, it := range items {
			if m, ok := it.(map[string]any); ok {
				out[i] = m
			} else {
				out[i] = record{}
			}
		}
		return out, nil
	}
	r := csv.NewReader(bytes.NewReader(content))
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil || len(rows) == 0 {
		return nil, fileError(http.StatusBadRequest, "invalid_file", "file is not a CSV with a header row")
	}
	header := rows[0]
	for i := range header {
		header[i] = strings.ToLower(strings.TrimSpace(header[i]))
	}
	var out []record
	for _, cells := range rows[1:] {
		rec := record{}
		blank := true
		for i, name := range header {
			if i < len(cells) {
				rec[name] = cells[i]
				blank = blank && strings.TrimSpace(cells[i]) == ""
			}
		}
		if !blank {
			out = append(out, rec)
		}
	}
	return out, nil
}

func str(v any) *string {
	if v == nil {
		return nil
	}
	s := strings.TrimSpace(fmt.Sprint(v))
	if s == "" {
		return nil
	}
	return &s
}

func containsSecret(records []record) bool {
	for _, rec := range records {
		for k, v := range rec {
			if secretHeader.MatchString(k) {
				return true
			}
			var s string
			switch x := v.(type) {
			case string:
				s = strings.TrimSpace(x)
			case []any:
				b, _ := json.Marshal(x)
				s = string(b)
			}
			for _, re := range secretValues {
				if s != "" && re.MatchString(s) {
					return true
				}
			}
		}
	}
	return false
}

// ID is the identity key of a wallet; EVM addresses are case-insensitive.
func ID(chain, address string) string {
	if chain != string(api.Solana) {
		address = strings.ToLower(address)
	}
	return chain + ":" + address
}

// Parse reads a CSV or JSON list and validates every row. existing reports
// whether a wallet is already a member.
func Parse(fileName string, content []byte, existing func(chain api.Chain, address string) bool) ([]api.ImportRow, error) {
	records, err := readRecords(fileName, content)
	if err != nil {
		return nil, err
	}
	if len(records) > MaxRows {
		return nil, fileError(http.StatusRequestEntityTooLarge, "too_many_rows", fmt.Sprintf("more than %d rows", MaxRows))
	}
	if containsSecret(records) {
		return nil, fileError(http.StatusBadRequest, "secret_detected", "file looks like it contains private keys")
	}
	seen := map[string]bool{}
	rows := make([]api.ImportRow, 0, len(records))
	for i, rec := range records {
		row := api.ImportRow{Line: int64(i + 2), Address: str(rec["address"]), Label: str(rec["label"]), Twitter: str(rec["twitter"]), Source: str(rec["source"]), Status: "valid", Tags: []string{}}
		if c := str(rec["chain"]); c != nil {
			row.Chain = ptr(strings.ToLower(*c))
		}
		switch tags := rec["tags"].(type) {
		case []any:
			for _, t := range tags {
				row.Tags = append(row.Tags, fmt.Sprint(t))
			}
		default:
			if s := str(tags); s != nil {
				for _, t := range strings.FieldsFunc(*s, func(r rune) bool { return r == '|' || r == ';' }) {
					if t = strings.TrimSpace(t); t != "" {
						row.Tags = append(row.Tags, t)
					}
				}
			}
		}
		rows = append(rows, classify(row, seen, existing))
	}
	return rows, nil
}

func classify(row api.ImportRow, seen map[string]bool, existing func(api.Chain, string) bool) api.ImportRow {
	fail := func(reason string) api.ImportRow {
		row.Status, row.Error = "invalid", &reason
		return row
	}
	if row.Chain == nil || !api.Chain(*row.Chain).Valid() {
		return fail("chain")
	}
	chain := api.Chain(*row.Chain)
	if row.Address == nil || (chain == api.Solana && !solanaAddr.MatchString(*row.Address)) || (chain != api.Solana && !evmAddr.MatchString(*row.Address)) {
		return fail("address")
	}
	if row.Twitter != nil && !twitter.MatchString(*row.Twitter) {
		return fail("twitter")
	}
	if row.Label != nil && len([]rune(*row.Label)) > 64 {
		return fail("label")
	}
	id := ID(*row.Chain, *row.Address)
	if seen[id] {
		row.Status = "duplicate"
		return row
	}
	seen[id] = true
	if existing(chain, *row.Address) {
		row.Status = "existing"
	}
	return row
}

// ValidAddress reports whether address is well formed for chain.
func ValidAddress(chain api.Chain, address string) bool {
	if chain == api.Solana {
		return solanaAddr.MatchString(address)
	}
	return chain.Valid() && evmAddr.MatchString(address)
}

func ptr[T any](v T) *T { return &v }

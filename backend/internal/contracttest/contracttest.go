// Package contracttest validates HTTP responses against api/openapi.yaml.
//
// Expectations come from the contract file, not from the implementation under test:
// a body must satisfy the declared schema for its status code and must not carry
// properties the contract does not declare.
package contracttest

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

var (
	loadOnce sync.Once
	loaded   *openapi3.T
	loadErr  error
)

// Spec returns the parsed and validated contract.
func Spec(t testing.TB) *openapi3.T {
	t.Helper()
	loadOnce.Do(func() {
		_, file, _, _ := runtime.Caller(0)
		path := filepath.Join(filepath.Dir(file), "../../../api/openapi.yaml")
		loader := openapi3.NewLoader()
		loaded, loadErr = loader.LoadFromFile(path)
		if loadErr == nil {
			loadErr = loaded.Validate(loader.Context)
		}
	})
	if loadErr != nil {
		t.Fatalf("load contract: %v", loadErr)
	}
	return loaded
}

// Check validates rec against the response declared for method and route
// (a path template such as "/api/v1/tokens/{chain}/{address}").
func Check(t testing.TB, method, route string, rec *httptest.ResponseRecorder) {
	t.Helper()
	if err := Validate(t, method, route, rec.Code, rec.Body.Bytes()); err != nil {
		t.Errorf("%s %s → %d violates contract: %v\nbody: %.400s", method, route, rec.Code, err, rec.Body.String())
	}
	if got := rec.Header().Get("X-Trace-Id"); len(got) != 32 {
		t.Errorf("%s %s: X-Trace-Id = %q, want 32 hex chars", method, route, got)
	}
}

// Validate reports how body deviates from the declared response schema.
func Validate(t testing.TB, method, route string, status int, body []byte) error {
	spec := Spec(t)
	item := spec.Paths.Value(route)
	if item == nil {
		return fmt.Errorf("route %s not in contract", route)
	}
	op := item.GetOperation(strings.ToUpper(method))
	if op == nil {
		return fmt.Errorf("method %s not declared for %s", method, route)
	}
	resp := op.Responses.Value(strconv.Itoa(status))
	if resp == nil || resp.Value == nil {
		return fmt.Errorf("status %d not declared", status)
	}
	media := resp.Value.Content.Get("application/json")
	if media == nil || media.Schema == nil {
		return fmt.Errorf("status %d declares no JSON body", status)
	}
	var data any
	if err := json.Unmarshal(body, &data); err != nil {
		return fmt.Errorf("body is not JSON: %w", err)
	}
	if err := media.Schema.Value.VisitJSON(data, openapi3.MultiErrors()); err != nil {
		return err
	}
	if extra := undeclared(media.Schema.Value, data, "$"); len(extra) > 0 {
		sort.Strings(extra)
		return fmt.Errorf("undeclared properties: %s", strings.Join(extra, ", "))
	}
	return nil
}

func undeclared(s *openapi3.Schema, data any, path string) []string {
	if s == nil || data == nil {
		return nil
	}
	if len(s.AllOf) > 0 {
		merged := &openapi3.Schema{Properties: openapi3.Schemas{}}
		for _, part := range s.AllOf {
			p := part.Value
			if len(p.AllOf) == 1 {
				p = p.AllOf[0].Value
			}
			for k, v := range p.Properties {
				merged.Properties[k] = v
			}
			if p.Items != nil {
				merged.Items = p.Items
			}
			if p.AdditionalProperties.Schema != nil {
				merged.AdditionalProperties = p.AdditionalProperties
			}
		}
		if len(merged.Properties) == 0 && merged.Items == nil {
			return undeclared(s.AllOf[0].Value, data, path)
		}
		s = merged
	}
	if len(s.OneOf) > 0 || len(s.AnyOf) > 0 {
		return nil
	}
	var out []string
	switch v := data.(type) {
	case map[string]any:
		for k, child := range v {
			prop, ok := s.Properties[k]
			switch {
			case ok:
				out = append(out, undeclared(prop.Value, child, path+"."+k)...)
			case s.AdditionalProperties.Schema != nil:
				out = append(out, undeclared(s.AdditionalProperties.Schema.Value, child, path+"."+k)...)
			case s.AdditionalProperties.Has != nil && *s.AdditionalProperties.Has:
			default:
				out = append(out, path+"."+k)
			}
		}
	case []any:
		if s.Items != nil {
			for i, child := range v {
				out = append(out, undeclared(s.Items.Value, child, fmt.Sprintf("%s[%d]", path, i))...)
			}
		}
	}
	return out
}

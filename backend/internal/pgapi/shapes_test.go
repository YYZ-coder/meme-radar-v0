package pgapi_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/meme-radar/radar/internal/contracttest"
	"github.com/meme-radar/radar/internal/httpapi"
	"github.com/meme-radar/radar/internal/mockdata"
)

func shapeProperty(s *openapi3.Schema, key string) (*openapi3.Schema, bool) {
	if s == nil {
		return nil, false
	}
	if p := s.Properties[key]; p != nil {
		return p.Value, true
	}
	for _, part := range s.AllOf {
		if p, ok := shapeProperty(part.Value, key); ok {
			return p, true
		}
	}
	if s.AdditionalProperties.Schema != nil {
		return s.AdditionalProperties.Schema.Value, true
	}
	return nil, false
}

func shapeRequired(s *openapi3.Schema, key string) bool {
	if s == nil {
		return false
	}
	for _, r := range s.Required {
		if r == key {
			return true
		}
	}
	for _, part := range s.AllOf {
		if shapeRequired(part.Value, key) {
			return true
		}
	}
	return false
}

// Only explicitly nullable/optional contract fields may differ in presence.
// Missing required keys, object/array confusion and primitive type drift fail.
func compareWireShape(t *testing.T, a, b any, s *openapi3.Schema, path string) {
	t.Helper()
	if a == nil || b == nil {
		if a == nil && b == nil {
			return
		}
		if s != nil && s.VisitJSON(nil) == nil {
			return
		}
		t.Errorf("%s unexpected null mismatch", path)
		return
	}
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok {
			t.Errorf("%s types %T/%T", path, a, b)
			return
		}
		keys := map[string]bool{}
		for k := range x {
			keys[k] = true
		}
		for k := range y {
			keys[k] = true
		}
		for k := range keys {
			p, declared := shapeProperty(s, k)
			av, ak := x[k]
			bv, bk := y[k]
			if !ak || !bk {
				if !declared || shapeRequired(s, k) {
					t.Errorf("%s.%s key presence differs", path, k)
				}
				continue
			}
			compareWireShape(t, av, bv, p, path+"."+k)
		}
	case []any:
		y, ok := b.([]any)
		if !ok {
			t.Errorf("%s types %T/%T", path, a, b)
			return
		}
		var item *openapi3.Schema
		if s != nil && s.Items != nil {
			item = s.Items.Value
		}
		if len(x) > 0 && len(y) > 0 {
			compareWireShape(t, x[0], y[0], item, path+"[]")
		}
	default:
		if reflect.TypeOf(a) != reflect.TypeOf(b) {
			t.Errorf("%s types %T/%T", path, a, b)
		}
	}
}

func TestPgShapesMatchMock(t *testing.T) {
	f := widestFixture(t)
	contractHoldings(t, f)
	pg := f.handler()
	mock := httpapi.New(mockdata.New(func() time.Time { return f.now }), httpapi.Options{Now: func() time.Time { return f.now }, AdminToken: "test-admin"})
	const route = "/api/v1/tokens/{chain}/{address}"
	target := "/api/v1/tokens/solana/" + solFixture
	cases := []routeRequest{
		{"GET", "/healthz", "/healthz", "", 200},
		{"GET", "/api/v1/market/natives", "/api/v1/market/natives", "", 200},
		{"GET", "/api/v1/search", "/api/v1/search?q=GX1J&chain=solana", "", 200},
		{"GET", route, target, "", 200},
		{"GET", route, target + "?currency=native", "", 200},
		{"GET", "/api/v1/smart-money/inflow", "/api/v1/smart-money/inflow?window=5m&limit=2", "", 200},
		{"POST", "/api/v1/tokens/batch", "/api/v1/tokens/batch", fmt.Sprintf(`{"window":"5m","currency":"usd","tokens":[{"chain":"solana","address":"%s"}]}`, solFixture), 200},
	}
	for _, suffix := range []string{"/activities", "/holders", "/pools", "/liquidity-events", "/smart-money"} {
		cases = append(cases, routeRequest{"GET", route + suffix, target + suffix + "?limit=2", "", 200})
	}
	cases = append(cases, routeRequest{"GET", route + "/candles", fmt.Sprintf("%s/candles?interval=1m&from=%d&to=%d&limit=2", target, apiTime.Add(-time.Minute).Unix(), apiTime.Unix()), "", 200})
	for _, c := range cases {
		t.Run(c.method+" "+c.target, func(t *testing.T) {
			a := requestPG(t, pg, c)
			b := requestPG(t, mock, c)
			var av, bv any
			if err := json.Unmarshal(a.Body.Bytes(), &av); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(b.Body.Bytes(), &bv); err != nil {
				t.Fatal(err)
			}
			s := contracttest.Spec(t).Paths.Value(c.route).GetOperation(strings.ToUpper(c.method)).Responses.Value("200").Value.Content.Get("application/json").Schema.Value
			compareWireShape(t, av, bv, s, "$")
		})
	}
}

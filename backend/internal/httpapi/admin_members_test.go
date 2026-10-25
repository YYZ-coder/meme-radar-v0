package httpapi_test

import (
	"net/http"
	"net/url"
	"slices"
	"testing"
	"time"
)

const (
	membersRoute    = "/api/v1/admin/smart-money/members"
	exclusionsRoute = "/api/v1/admin/smart-money/exclusions"
)

type member struct {
	Chain, Address string
	Tags, Sources  []string
	Excluded       bool
}

type members struct {
	ListVersion string `json:"list_version"`
	Total       int
	Items       []member
	NextCursor  *string `json:"next_cursor"`
}

func TestMembersFilterAndPage(t *testing.T) {
	h := newHarness(t)
	var all members
	h.get(membersRoute, membersRoute+"?limit=100", http.StatusOK, &all, true)
	if all.Total == 0 || all.Total != len(all.Items) {
		t.Fatalf("members total %d items %d", all.Total, len(all.Items))
	}
	seen := map[string]bool{}
	var p members
	h.get(membersRoute, membersRoute+"?limit=10", http.StatusOK, &p, true)
	for p.NextCursor != nil {
		for _, m := range p.Items {
			if seen[m.Address] {
				t.Fatalf("member %s repeated", m.Address)
			}
			seen[m.Address] = true
		}
		c := *p.NextCursor
		p = members{}
		h.get(membersRoute, membersRoute+"?limit=10&cursor="+url.QueryEscape(c), http.StatusOK, &p, true)
	}
	if len(seen)+len(p.Items) != all.Total {
		t.Errorf("paged %d members, total %d", len(seen)+len(p.Items), all.Total)
	}
	for q, ok := range map[string]func(member) bool{
		"chain=solana":  func(m member) bool { return m.Chain == "solana" },
		"source=import": func(m member) bool { return slices.Contains(m.Sources, "import") },
		"tag=kol":       func(m member) bool { return slices.Contains(m.Tags, "kol") },
	} {
		var f members
		h.get(membersRoute, membersRoute+"?limit=100&"+q, http.StatusOK, &f, true)
		if len(f.Items) == 0 {
			t.Errorf("%s matched nothing", q)
		}
		for _, m := range f.Items {
			if !ok(m) {
				t.Errorf("%s let through %+v", q, m)
			}
		}
	}
}

func (h *harness) change(method, target, route, key string, body any, status int) string {
	h.t.Helper()
	hdr := map[string]string{"Content-Type": "application/json"}
	if key != "" {
		hdr["Idempotency-Key"] = key
	}
	r := req{method: method, target: target, header: hdr, admin: true}
	if body != nil {
		r.body = jsonBody(body)
	}
	var c struct {
		ListVersion string `json:"list_version"`
	}
	h.expect(method, route, h.do(r), status, &c)
	return c.ListVersion
}

// D125 + Idempotency-Key contract: exclusion keeps the record but removes the wallet
// from smart-money identity once the new version is active.
func TestExclusionLifecycle(t *testing.T) {
	h := newHarness(t)
	var all members
	h.get(membersRoute, membersRoute+"?limit=100&chain=solana", http.StatusOK, &all, true)
	m := all.Items[0]
	body := map[string]string{"chain": m.Chain, "address": m.Address, "reason": "bot"}

	var e errorBody
	rec := h.do(req{method: http.MethodPost, target: exclusionsRoute, body: jsonBody(body), admin: true})
	h.expect(http.MethodPost, exclusionsRoute, rec, http.StatusBadRequest, &e)
	if e.Error.Field == nil || *e.Error.Field != "Idempotency-Key" {
		t.Errorf("missing key field = %v", e.Error.Field)
	}
	h.change(http.MethodPost, exclusionsRoute, exclusionsRoute, "not-a-uuid", body, http.StatusBadRequest)
	h.change(http.MethodPost, exclusionsRoute, exclusionsRoute, "", map[string]string{"chain": m.Chain, "address": m.Address, "reason": ""}, http.StatusBadRequest)

	const key = "0d6f6a8e-3f0b-4b7e-9c1a-2a7b1c9d4e01"
	v := h.change(http.MethodPost, exclusionsRoute, exclusionsRoute, key, body, http.StatusAccepted)
	if again := h.change(http.MethodPost, exclusionsRoute, exclusionsRoute, key, body, http.StatusAccepted); again != v {
		t.Errorf("replayed key returned %s, want %s", again, v)
	}
	h.change(http.MethodPost, exclusionsRoute, exclusionsRoute, key, map[string]string{"chain": m.Chain, "address": m.Address, "reason": "other"}, http.StatusBadRequest)

	h.clock.Advance(3 * time.Second)
	var ex struct {
		Items []struct{ Address, Reason string }
	}
	h.get(exclusionsRoute, exclusionsRoute, http.StatusOK, &ex, true)
	if len(ex.Items) != 1 || ex.Items[0].Address != m.Address {
		t.Fatalf("exclusions = %+v", ex.Items)
	}
	h.get(membersRoute, membersRoute+"?limit=100&chain=solana", http.StatusOK, &all, true)
	if i := slices.IndexFunc(all.Items, func(x member) bool { return x.Address == m.Address }); i < 0 || !all.Items[i].Excluded {
		t.Errorf("excluded member record missing or not flagged")
	}
	var s struct {
		Wallets []struct {
			Address      string
			IsSmartMoney bool `json:"is_smart_money"`
		}
	}
	h.get("/api/v1/search", "/api/v1/search?q="+m.Address, http.StatusOK, &s)
	if len(s.Wallets) != 1 || s.Wallets[0].IsSmartMoney {
		t.Errorf("excluded wallet search = %+v, want is_smart_money false", s.Wallets)
	}

	target := exclusionsRoute + "/" + m.Chain + "/" + m.Address
	h.change(http.MethodDelete, target, exclusionsRoute+"/{chain}/{address}", "1d6f6a8e-3f0b-4b7e-9c1a-2a7b1c9d4e02", nil, http.StatusAccepted)
	h.clock.Advance(3 * time.Second)
	h.get(exclusionsRoute, exclusionsRoute, http.StatusOK, &ex, true)
	if len(ex.Items) != 0 {
		t.Errorf("restored wallet still excluded: %+v", ex.Items)
	}
	h.change(http.MethodDelete, target, exclusionsRoute+"/{chain}/{address}", "2d6f6a8e-3f0b-4b7e-9c1a-2a7b1c9d4e03", nil, http.StatusNotFound)
}

// Removing the import source of an import-only member removes it; auto-only members are 404.
func TestRemoveMember(t *testing.T) {
	h := newHarness(t)
	var all members
	h.get(membersRoute, membersRoute+"?limit=100", http.StatusOK, &all, true)
	find := func(pred func(member) bool) member {
		i := slices.IndexFunc(all.Items, pred)
		if i < 0 {
			t.Fatal("no member matches")
		}
		return all.Items[i]
	}
	importOnly := find(func(m member) bool { return slices.Equal(m.Sources, []string{"import"}) })
	autoOnly := find(func(m member) bool { return slices.Equal(m.Sources, []string{"auto"}) })
	route := membersRoute + "/{chain}/{address}"
	h.change(http.MethodDelete, membersRoute+"/"+importOnly.Chain+"/"+importOnly.Address, route, "3d6f6a8e-3f0b-4b7e-9c1a-2a7b1c9d4e04", nil, http.StatusAccepted)
	h.change(http.MethodDelete, membersRoute+"/"+autoOnly.Chain+"/"+autoOnly.Address, route, "4d6f6a8e-3f0b-4b7e-9c1a-2a7b1c9d4e05", nil, http.StatusNotFound)
	h.clock.Advance(3 * time.Second)
	var after members
	h.get(membersRoute, membersRoute+"?limit=100", http.StatusOK, &after, true)
	if after.Total != all.Total-1 || slices.ContainsFunc(after.Items, func(m member) bool { return m.Address == importOnly.Address }) {
		t.Errorf("after removal total %d (was %d)", after.Total, all.Total)
	}
}

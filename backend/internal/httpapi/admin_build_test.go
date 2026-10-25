package httpapi_test

import (
	"net/http"
	"testing"
	"time"
)

const buildRoute = "/api/v1/admin/smart-money/list-builds/{version}"

type buildStatus struct {
	ListVersion     string `json:"list_version"`
	ActiveVersion   string `json:"active_version"`
	Status          string
	BuildStatus     string  `json:"build_status"`
	BlockingVersion *string `json:"blocking_version"`
	ActivatedAt     *string `json:"activated_at"`
}

func (h *harness) applySample() string {
	h.t.Helper()
	var rep importReport
	h.upload("list.csv", sampleCSV, http.StatusCreated, &rep)
	var change struct {
		ListVersion string `json:"list_version"`
	}
	rec := h.do(req{method: http.MethodPost, target: importsRoute + "/" + rep.ID + "/apply", admin: true})
	h.expect(http.MethodPost, importsRoute+"/{id}/apply", rec, http.StatusAccepted, &change)
	return change.ListVersion
}

func (h *harness) build(version string) buildStatus {
	h.t.Helper()
	var st buildStatus
	h.get(buildRoute, "/api/v1/admin/smart-money/list-builds/"+version, http.StatusOK, &st, true)
	return st
}

// D124 / 01-api §5: the old list stays active while a version prepares; activation
// switches list_version everywhere and stale batch refreshes get 410.
func TestListBuildLifecycle(t *testing.T) {
	h := newHarness(t)
	var p struct {
		QueryID     string `json:"query_id"`
		ListVersion string `json:"list_version"`
		Items       []inflowRow
	}
	h.get(inflowRoute, inflowRoute+"?window=1h&limit=3", http.StatusOK, &p)
	keys := []tokenKey{{p.Items[0].Token.Chain, p.Items[0].Token.Address}}

	v := h.applySample()
	if st := h.build(v); st.BuildStatus != "queued" || st.ActiveVersion != "42" || st.Status != "building" {
		t.Fatalf("new build = %+v", st)
	}
	h.batch(map[string]any{"window": "1h", "tokens": keys, "query_id": p.QueryID, "list_version": p.ListVersion}, http.StatusOK, nil)

	h.clock.Advance(600 * time.Millisecond)
	if st := h.build(v); st.BuildStatus != "running" {
		t.Errorf("after 0.6s build_status = %s, want running", st.BuildStatus)
	}
	h.clock.Advance(2 * time.Second)
	st := h.build(v)
	if st.BuildStatus != "activated" || st.Status != "active" || st.ActiveVersion != v || st.ActivatedAt == nil {
		t.Fatalf("after 2.6s build = %+v, want activated and active", st)
	}
	var ov struct {
		ListVersion string `json:"list_version"`
	}
	h.get("/api/v1/smart-money/overview", "/api/v1/smart-money/overview?window=1h", http.StatusOK, &ov)
	if ov.ListVersion != v {
		t.Errorf("overview list_version = %s, want %s", ov.ListVersion, v)
	}
	h.batch(map[string]any{"window": "1h", "tokens": keys, "query_id": p.QueryID, "list_version": p.ListVersion}, http.StatusGone, nil)

	var retried buildStatus
	rec := h.do(req{method: http.MethodPost, target: "/api/v1/admin/smart-money/list-builds/" + v + "/retry", admin: true})
	h.expect(http.MethodPost, buildRoute+"/retry", rec, http.StatusOK, &retried)
	h.get(buildRoute, "/api/v1/admin/smart-money/list-builds/999", http.StatusNotFound, nil, true)
}

// A version queued behind an unfinished parent reports the parent as blocking.
func TestListBuildBlockedByParent(t *testing.T) {
	h := newHarness(t)
	first := h.applySample()
	second := h.applySample()
	if st := h.build(second); st.BlockingVersion == nil || *st.BlockingVersion != first {
		t.Fatalf("second build blocking = %v, want %s", st.BlockingVersion, first)
	}
	h.clock.Advance(5 * time.Second)
	if st := h.build(second); st.BuildStatus != "activated" || st.ActiveVersion != second {
		t.Errorf("chain of builds did not activate in order: %+v", st)
	}
	if st := h.build(first); st.Status != "retired" {
		t.Errorf("superseded version status = %s, want retired", st.Status)
	}
}

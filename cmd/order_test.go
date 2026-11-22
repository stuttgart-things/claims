package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stuttgart-things/claims/internal/templates"
)

// fakeOrderService is machinery-order-service and claim-machinery-api in one.
type fakeOrderService struct {
	mu        sync.Mutex
	created   []map[string]any
	auth      []string
	gets      int
	amGets    int
	tdGets    int
	teardowns []map[string]any
}

func (f *fakeOrderService) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/claim-templates", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{
			"metadata": map[string]any{"name": "vm", "title": "A VM"},
			"spec": map[string]any{"type": "vm", "parameters": []any{
				map[string]any{"name": "name", "type": "string", "required": true},
				map[string]any{"name": "size", "type": "string", "required": true, "default": "small", "enum": []string{"small", "large"}},
				map[string]any{"name": "disk", "type": "string", "showWhen": map[string]any{"size": []string{"large"}}},
			}},
		}}})
	})
	mux.HandleFunc("GET /api/v1/management-clusters", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"name": "mc-a", "lab": "lab-a", "cordoned": false,
			"autoMerge": map[string]any{"rules": []any{map[string]any{"name": "personas", "groups": []string{"group:default/ai-personas"},
				"templates": []string{"vm"}, "parameters": map[string]any{"size": []string{"small"}}}}}}}})
	})
	mux.HandleFunc("POST /api/v1/orders", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.created = append(f.created, body)
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		f.mu.Unlock()
		if body["autoMerge"] == true && body["parameters"].(map[string]any)["size"] != "small" {
			w.WriteHeader(403)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "auto-merge not allowed on mc-a: personas: size=large not in [small]"})
			return
		}
		if body["managementCluster"] == "denied" {
			w.WriteHeader(403)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "forbidden: template orderRoles"})
			return
		}
		spec := map[string]any{"template": body["template"], "placement": map[string]any{"managementCluster": body["managementCluster"]}}
		if body["autoMerge"] == true {
			spec["autoMerge"] = map[string]any{"rule": "personas"}
		}
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"metadata": map[string]any{"name": "ord-1"},
			"spec":     spec,
			"status":   map[string]any{"phase": "Ordered", "message": "accepted"},
		})
	})
	mux.HandleFunc("GET /api/v1/orders/ord-am", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.amGets++
		n := f.amGets
		f.mu.Unlock()
		steps := []struct{ phase, msg string }{
			{"PROpen", "pull request https://example.com/pr/8"},
			{"PROpen", "pull request https://example.com/pr/8; auto-merge: waiting for checks: validate"},
			{"PROpen", "pull request https://example.com/pr/8; auto-merge: waiting for checks: validate"},
			{"PROpen", "pull request https://example.com/pr/8; auto-merge: cannot read checks: 403"},
			{"Ready", "ready"},
		}
		st := steps[min(n-1, len(steps)-1)]
		_ = json.NewEncoder(w).Encode(map[string]any{
			"metadata": map[string]any{"name": "ord-am"},
			"spec":     map[string]any{"autoMerge": map[string]any{"rule": "personas"}},
			"status": map[string]any{"phase": st.phase, "message": st.msg,
				"delivery":   map[string]any{"pullRequest": map[string]any{"number": 8, "url": "https://example.com/pr/8"}},
				"conditions": []any{map[string]any{"type": "AutoMerge", "status": "Unknown", "reason": "Waiting", "message": "403 Resource not accessible by integration"}}},
		})
	})
	// ord-td: a teardown that goes through; ord-wd: one that is withdrawn.
	mux.HandleFunc("POST /api/v1/orders/{id}/teardown", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.teardowns = append(f.teardowns, body)
		f.mu.Unlock()
		td := map[string]any{"attempt": 1, "requestedBy": "user:default/persona", "reason": body["reason"]}
		if body["autoMerge"] == true {
			td["autoMerge"] = map[string]any{"rule": "personas"}
		}
		w.WriteHeader(202)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"metadata": map[string]any{"name": r.PathValue("id")},
			"spec":     map[string]any{"teardown": td},
			"status":   map[string]any{"phase": "Ready"},
		})
	})
	for _, id := range []string{"ord-td", "ord-wd"} {
		mux.HandleFunc("GET /api/v1/orders/"+id, func(w http.ResponseWriter, r *http.Request) {
			f.mu.Lock()
			f.tdGets++
			n := f.tdGets
			f.mu.Unlock()
			type step struct{ phase, outcome, msg string }
			steps := []step{{"Ready", "", "ready"}, {"TeardownPROpen", "", "teardown pull request https://example.com/pr/9"},
				{"TearingDown", "", "teardown merged"}, {"Deleted", "Deleted", "torn down"}}
			if id == "ord-wd" {
				steps = []step{{"Ready", "", "ready"}, {"TeardownPROpen", "", "teardown pull request https://example.com/pr/9"},
					{"Ready", "Withdrawn", "teardown withdrawn: pull request closed without a merge"}}
			}
			st := steps[min(n-1, len(steps)-1)]
			status := map[string]any{"phase": st.phase, "message": st.msg, "live": map[string]any{"address": "192.0.2.11"}}
			if st.phase != "Ready" || st.outcome != "" {
				status["teardown"] = map[string]any{"attempt": 1, "outcome": st.outcome,
					"delivery": map[string]any{"pullRequest": map[string]any{"number": 9, "url": "https://example.com/pr/9"}}}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"metadata": map[string]any{"name": id},
				"spec":     map[string]any{"teardown": map[string]any{"attempt": 1, "requestedBy": "user:default/persona", "autoMerge": map[string]any{"rule": "personas"}}},
				"status":   status,
			})
		})
	}
	mux.HandleFunc("GET /api/v1/orders/ord-building", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"metadata": map[string]any{"name": "ord-building"},
			"status":   map[string]any{"phase": "Building", "live": map[string]any{"found": true, "address": "192.0.2.12"}},
		})
	})
	mux.HandleFunc("GET /api/v1/orders/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "ord-1" {
			w.WriteHeader(404)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "order not found"})
			return
		}
		f.mu.Lock()
		f.gets++
		phase := []string{"Ordered", "PROpen", "Ready"}[min(f.gets-1, 2)]
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"metadata": map[string]any{"name": "ord-1"},
			"status": map[string]any{"phase": phase, "live": map[string]any{"address": "192.0.2.10"},
				"delivery": map[string]any{"pullRequest": map[string]any{"number": 7, "url": "https://example.com/pr/7"}}},
		})
	})
	return mux
}

func setupOrderTest(t *testing.T) *fakeOrderService {
	t.Helper()
	f := &fakeOrderService{}
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAIMS_CONFIG_FILE", filepath.Join(t.TempDir(), "config.yaml"))
	t.Setenv("CLAIM_API_URL", srv.URL)
	t.Setenv(EnvOrdersURL, srv.URL)
	t.Setenv("CLAIM_API_TOKEN", "test-token")
	reset := func() {
		orderCluster, orderReason, orderParams, orderParamsFile = "", "", nil, ""
		orderAutoMerge = false
		orderWait, orderNonInter, outputFlag = false, true, "table"
		orderWaitTimeout, watchInterval, watchUntil = time.Minute, time.Millisecond, ""
		teardownReason, teardownAutoMerge, teardownWait, teardownTimeout = "", false, false, time.Minute
		ordersURLFlag, orderAPIURLFlag = "", ""
	}
	reset()
	t.Cleanup(reset)
	return f
}

func TestOrderProgrammatic(t *testing.T) {
	f := setupOrderTest(t)

	// Missing pieces are usage errors that name the flag (exit 2).
	for name, tc := range map[string]struct {
		args    []string
		cluster string
		params  []string
		want    string
	}{
		"no template": {nil, "mc-a", nil, "claims templates"},
		"no cluster":  {[]string{"vm"}, "", []string{"name=x"}, "--cluster is required"},
		"no name":     {[]string{"vm"}, "mc-a", nil, "missing required parameters: name"},
	} {
		t.Run(name, func(t *testing.T) {
			orderCluster, orderParams = tc.cluster, tc.params
			err := runOrder(&bytes.Buffer{}, tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) || exitCode(err) != exitCodeUsage {
				t.Errorf("err %v (exit %d)", err, exitCode(err))
			}
		})
	}
	if len(f.created) != 0 {
		t.Fatalf("ordered despite errors: %v", f.created)
	}

	// A good order: inactive parameters dropped, token sent, JSON out.
	orderCluster, orderReason, outputFlag = "mc-a", "test", "json"
	orderParams = []string{"name=demo", "disk=100"}
	var out bytes.Buffer
	if err := runOrder(&out, []string{"vm"}); err != nil {
		t.Fatal(err)
	}
	got := f.created[0]
	p := got["parameters"].(map[string]any)
	if got["template"] != "vm" || got["managementCluster"] != "mc-a" || got["reason"] != "test" || p["name"] != "demo" {
		t.Errorf("request %v", got)
	}
	if _, ok := p["disk"]; ok {
		t.Error("disk (showWhen size=large) was sent for size small")
	}
	if f.auth[0] != "Bearer test-token" {
		t.Errorf("auth %q", f.auth[0])
	}
	var o map[string]any
	if err := json.Unmarshal(out.Bytes(), &o); err != nil || o["status"].(map[string]any)["phase"] != "Ordered" {
		t.Errorf("json out %q: %v", out.String(), err)
	}

	// The server's 403 reason passes through, exit 3.
	orderCluster, outputFlag = "denied", "table"
	err := runOrder(&bytes.Buffer{}, []string{"vm"})
	if err == nil || !strings.Contains(err.Error(), "orderRoles") || exitCode(err) != exitCodeDenied {
		t.Errorf("denied: %v (exit %d)", err, exitCode(err))
	}
}

func TestOrderWaitAndOrders(t *testing.T) {
	setupOrderTest(t)
	orderCluster, orderParams, orderWait = "mc-a", []string{"name=demo"}, true
	var out bytes.Buffer
	if err := runOrder(&out, []string{"vm"}); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "PROpen") || !strings.Contains(s, "https://example.com/pr/7 (waits for a merge)") || !strings.Contains(s, "address 192.0.2.10") {
		t.Errorf("wait output:\n%s", s)
	}

	out.Reset()
	if err := runOrdersGet(&out, "ord-1"); err != nil || !strings.Contains(out.String(), "Pull request:") {
		t.Errorf("get: %v\n%s", err, out.String())
	}
	err := runOrdersGet(&bytes.Buffer{}, "ord-nope")
	if exitCode(err) != exitCodeNotFound {
		t.Errorf("not found: %v (exit %d)", err, exitCode(err))
	}

	out.Reset()
	if err := runClusters(&out); err != nil || !strings.Contains(out.String(), "mc-a") {
		t.Errorf("clusters: %v\n%s", err, out.String())
	}
	out.Reset()
	if err := runTemplates(&out, []string{"vm"}); err != nil || !strings.Contains(out.String(), "size=large") {
		t.Errorf("templates vm: %v\n%s", err, out.String())
	}
}

func TestOrderNeedsServiceURL(t *testing.T) {
	setupOrderTest(t)
	t.Setenv(EnvOrdersURL, "")
	err := runClusters(&bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "--orders-url") {
		t.Errorf("err %v", err)
	}
}

func TestRenderOnlyNotify(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(map[string]any{"rendered": "kind: X\n"})
	}))
	defer srv.Close()
	c := templates.NewClient(srv.URL)
	if _, err := c.RenderTemplate("vm", nil); err != nil || query != "" {
		t.Errorf("order: %q %v", query, err)
	}
	c.RenderOnly = true
	if _, err := c.RenderTemplate("vm", nil); err != nil || query != "notify=false" {
		t.Errorf("render only: %q %v", query, err)
	}
}

func TestExitCodes(t *testing.T) {
	for err, want := range map[error]int{
		errors.New("x"):                  exitCodeError,
		usagef("--x"):                    exitCodeUsage,
		&templates.APIError{Status: 401}: exitCodeDenied,
		&templates.APIError{Status: 404}: exitCodeNotFound,
		&templates.APIError{Status: 500}: exitCodeError,
	} {
		if got := exitCode(err); got != want {
			t.Errorf("%v: %d, want %d", err, got, want)
		}
	}
}

func TestOrderAutoMerge(t *testing.T) {
	f := setupOrderTest(t)
	orderCluster, orderAutoMerge = "mc-a", true

	// A rule covers it: autoMerge is sent and the order says so.
	orderParams = []string{"name=demo", "size=small"}
	var out bytes.Buffer
	if err := runOrder(&out, []string{"vm"}); err != nil {
		t.Fatal(err)
	}
	if f.created[0]["autoMerge"] != true {
		t.Errorf("request %v", f.created[0])
	}
	if !strings.Contains(out.String(), "Auto-merge: the service merges") || !strings.Contains(out.String(), "(rule personas)") {
		t.Errorf("output:\n%s", out.String())
	}

	// No rule covers it: the service's reason, exit 3 (denied).
	orderParams = []string{"name=demo", "size=large"}
	err := runOrder(&bytes.Buffer{}, []string{"vm"})
	if err == nil || !strings.Contains(err.Error(), "size=large not in [small]") || exitCode(err) != exitCodeDenied {
		t.Errorf("err %v (exit %d)", err, exitCode(err))
	}

	// Without the flag nothing is asked for.
	orderAutoMerge, orderParams = false, []string{"name=demo", "size=large"}
	if err := runOrder(&bytes.Buffer{}, []string{"vm"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.created[2]["autoMerge"]; ok {
		t.Errorf("autoMerge sent without the flag: %v", f.created[2])
	}

	// claims clusters shows the rules.
	out.Reset()
	if err := runClusters(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "1 rule(s)") || !strings.Contains(out.String(), "personas: group:default/ai-personas; templates vm; size=small") {
		t.Errorf("clusters:\n%s", out.String())
	}
}

func TestOrdersWatchMessages(t *testing.T) {
	setupOrderTest(t)
	var out bytes.Buffer
	if err := runOrdersWatch(&out, "ord-am"); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	// PROpen first (with the PR suffix), two new messages, Ready; the repeated
	// message is not printed again.
	if len(lines) != 4 {
		t.Fatalf("%d lines:\n%s", len(lines), out.String())
	}
	if !strings.Contains(lines[0], "(auto-merge once its checks pass)") ||
		!strings.Contains(lines[1], "auto-merge: waiting for checks: validate") || strings.Contains(lines[1], "once its checks pass") ||
		!strings.Contains(lines[2], "cannot read checks: 403") || !strings.Contains(lines[3], "Ready") {
		t.Errorf("watch:\n%s", out.String())
	}

	out.Reset()
	outputFlag = "table"
	if err := runOrdersGet(&out, "ord-am"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Conditions:\n  AutoMerge=Unknown Waiting: 403 Resource not accessible by integration") {
		t.Errorf("get:\n%s", out.String())
	}
}

func TestOrdersTeardown(t *testing.T) {
	f := setupOrderTest(t)
	teardownReason, teardownAutoMerge, teardownWait = "test done", true, true
	var out bytes.Buffer
	if err := runOrdersTeardown(&out, "ord-td"); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if len(f.teardowns) != 1 || f.teardowns[0]["reason"] != "test done" || f.teardowns[0]["autoMerge"] != true {
		t.Errorf("request %v", f.teardowns)
	}
	s := out.String()
	// Ready is not the end while the teardown runs.
	for _, want := range []string{"rule personas", "TeardownPROpen", "https://example.com/pr/9 (auto-merge once its checks pass)", "TearingDown", "Deleted"} {
		if !strings.Contains(s, want) {
			t.Errorf("output misses %q:\n%s", want, s)
		}
	}

	out.Reset()
	if err := runOrdersGet(&out, "ord-td"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Teardown:", "auto-merge rule personas, deleted", "Teardown PR:", "192.0.2.11 (gone; last seen)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("get misses %q:\n%s", want, out.String())
		}
	}
}

func TestOrdersTeardownWithdrawn(t *testing.T) {
	setupOrderTest(t)
	teardownWait = true
	var out bytes.Buffer
	err := runOrdersTeardown(&out, "ord-wd")
	if err == nil || !strings.Contains(err.Error(), "teardown of order ord-wd withdrawn") {
		t.Fatalf("want withdrawn, got %v\n%s", err, out.String())
	}
}

func TestOrdersGetAddressNotReady(t *testing.T) {
	setupOrderTest(t)
	var out bytes.Buffer
	if err := runOrdersGet(&out, "ord-building"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "192.0.2.12 (not ready yet: phase Building; ssh may be refused until Ready)") {
		t.Errorf("get:\n%s", out.String())
	}
}

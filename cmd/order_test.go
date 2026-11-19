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
	mu      sync.Mutex
	created []map[string]any
	auth    []string
	gets    int
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

// Package orders is the client of machinery-order-service's REST API
// (docs/adr/0002-order-through-the-order-service.md): orders go through the
// service, which renders with the caller's token and opens the pull request
// with its own GitHub identity. claims never touches git for them.
package orders

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/stuttgart-things/claims/internal/templates"
)

// Client talks to one machinery-order-service.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

// New returns a client for baseURL using httpClient (which carries the auth).
func New(baseURL string, httpClient *http.Client) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), HTTPClient: httpClient}
}

// CreateRequest is the body of POST /api/v1/orders.
type CreateRequest struct {
	Template          string         `json:"template"`
	Parameters        map[string]any `json:"parameters,omitempty"`
	ManagementCluster string         `json:"managementCluster"`
	Reason            string         `json:"reason,omitempty"`
	// AutoMerge asks the service to merge the pull request once its checks
	// pass; the service refuses (403) unless a rule of the cluster allows it.
	AutoMerge bool `json:"autoMerge,omitempty"`
}

// Final phases: nothing more happens to the order without someone acting.
var Final = []string{"Ready", "Failed", "Cancelled", "Deleted"}

// IsFinal reports whether phase is one of Final.
func IsFinal(phase string) bool {
	for _, p := range Final {
		if p == phase {
			return true
		}
	}
	return false
}

// Order is the part of an Order claims shows; Raw is the full object as the
// service sent it (for -o json).
type Order struct {
	Metadata struct {
		Name              string    `json:"name"`
		CreationTimestamp time.Time `json:"creationTimestamp"`
		ResourceVersion   string    `json:"resourceVersion,omitempty"`
	} `json:"metadata"`
	Spec struct {
		Template    string `json:"template"`
		RequestedBy string `json:"requestedBy,omitempty"`
		Reason      string `json:"reason,omitempty"`
		Placement   struct {
			ManagementCluster string `json:"managementCluster"`
		} `json:"placement"`
		Cancel    bool `json:"cancel,omitempty"`
		AutoMerge *struct {
			Rule string `json:"rule"`
		} `json:"autoMerge,omitempty"`
	} `json:"spec"`
	Status struct {
		Phase    string `json:"phase,omitempty"`
		Message  string `json:"message,omitempty"`
		Delivery *struct {
			PullRequest *struct {
				Number int    `json:"number"`
				URL    string `json:"url"`
				State  string `json:"state"`
			} `json:"pullRequest,omitempty"`
		} `json:"delivery,omitempty"`
		Live *struct {
			Found         bool   `json:"found"`
			Ready         bool   `json:"ready"`
			StatusMessage string `json:"statusMessage,omitempty"`
			Address       string `json:"address,omitempty"`
		} `json:"live,omitempty"`
		History []struct {
			Phase   string    `json:"phase"`
			Time    time.Time `json:"time"`
			Message string    `json:"message,omitempty"`
		} `json:"history,omitempty"`
	} `json:"status"`

	Raw json.RawMessage `json:"-"`
}

// ID is the order ID.
func (o *Order) ID() string { return o.Metadata.Name }

// Phase is the phase, "Pending" while the service has not set one yet.
func (o *Order) Phase() string {
	if o.Status.Phase == "" {
		return "Pending"
	}
	return o.Status.Phase
}

// PRURL is the pull request's URL, "" before there is one.
func (o *Order) PRURL() string {
	if d := o.Status.Delivery; d != nil && d.PullRequest != nil {
		return d.PullRequest.URL
	}
	return ""
}

// Address is what machinery reports for the live resource (a VM's IP).
func (o *Order) Address() string {
	if l := o.Status.Live; l != nil {
		return l.Address
	}
	return ""
}

func decodeOrder(raw []byte) (*Order, error) {
	o := &Order{}
	if err := json.Unmarshal(raw, o); err != nil {
		return nil, fmt.Errorf("decode order: %w", err)
	}
	o.Raw = append(json.RawMessage(nil), raw...)
	return o, nil
}

// Cluster is a management cluster orders can go to.
type Cluster struct {
	Name       string `json:"name"`
	Lab        string `json:"lab,omitempty"`
	Cordoned   bool   `json:"cordoned"`
	Repository string `json:"repository,omitempty"`
	OrdersPath string `json:"ordersPath,omitempty"`
	// AutoMerge says who may order with --auto-merge on this cluster.
	AutoMerge *AutoMerge `json:"autoMerge,omitempty"`
}

// AutoMerge are a cluster's auto-merge rules.
type AutoMerge struct {
	Method string          `json:"method,omitempty"`
	Rules  []AutoMergeRule `json:"rules,omitempty"`
}

// AutoMergeRule allows auto-merge for some orderers, templates and
// parameter values.
type AutoMergeRule struct {
	Name       string              `json:"name"`
	Users      []string            `json:"users,omitempty"`
	Groups     []string            `json:"groups,omitempty"`
	Templates  []string            `json:"templates,omitempty"`
	Parameters map[string][]string `json:"parameters,omitempty"`
}

// String is the rule in one line, e.g.
// "persona-test-vms: group:default/ai-personas; templates vm; size=small".
func (r AutoMergeRule) String() string {
	parts := []string{strings.Join(append(append([]string{}, r.Users...), r.Groups...), ", ")}
	if len(r.Templates) > 0 {
		parts = append(parts, "templates "+strings.Join(r.Templates, ", "))
	}
	keys := make([]string, 0, len(r.Parameters))
	for k := range r.Parameters {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts = append(parts, k+"="+strings.Join(r.Parameters[k], "|"))
	}
	return r.Name + ": " + strings.Join(parts, "; ")
}

func (c *Client) do(method, path string, body any, want int) ([]byte, error) {
	var rd *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(raw)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, c.BaseURL+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("order service: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != want {
		return nil, templates.StatusError(resp)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Create places an order.
func (c *Client) Create(req CreateRequest) (*Order, error) {
	raw, err := c.do(http.MethodPost, "/api/v1/orders", req, http.StatusCreated)
	if err != nil {
		return nil, err
	}
	return decodeOrder(raw)
}

// ListOptions narrow a list.
type ListOptions struct {
	Mine    bool
	Phase   string
	Cluster string
}

// List returns the orders the caller may see, newest first.
func (c *Client) List(opt ListOptions) ([]*Order, error) {
	q := url.Values{}
	if opt.Mine {
		q.Set("mine", "true")
	}
	if opt.Phase != "" {
		q.Set("phase", opt.Phase)
	}
	if opt.Cluster != "" {
		q.Set("cluster", opt.Cluster)
	}
	path := "/api/v1/orders"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	raw, err := c.do(http.MethodGet, path, nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("decode orders: %w", err)
	}
	out := make([]*Order, 0, len(list.Items))
	for _, it := range list.Items {
		o, err := decodeOrder(it)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, nil
}

// Get reads one order.
func (c *Client) Get(id string) (*Order, error) {
	raw, err := c.do(http.MethodGet, "/api/v1/orders/"+url.PathEscape(id), nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	return decodeOrder(raw)
}

// Cancel asks to withdraw an order (before its merge).
func (c *Client) Cancel(id string) (*Order, error) {
	raw, err := c.do(http.MethodPost, "/api/v1/orders/"+url.PathEscape(id)+"/cancel", nil, http.StatusAccepted)
	if err != nil {
		var ae *templates.APIError
		// 200: it was cancelled already.
		if asAPIError(err, &ae) && ae.Status == http.StatusOK {
			return c.Get(id)
		}
		return nil, err
	}
	return decodeOrder(raw)
}

// Clusters lists the management clusters.
func (c *Client) Clusters() ([]Cluster, error) {
	raw, err := c.do(http.MethodGet, "/api/v1/management-clusters", nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []Cluster `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("decode clusters: %w", err)
	}
	return list.Items, nil
}

func asAPIError(err error, target **templates.APIError) bool {
	ae, ok := err.(*templates.APIError)
	if ok {
		*target = ae
	}
	return ok
}

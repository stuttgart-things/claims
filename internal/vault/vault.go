// Package vault is the Vault side of `claims secret` (claims#122): KV v2
// read, write and list on one lab's Vault, with token or AppRole auth
// (OIDC comes later). Values are handled as strings and never logged.
package vault

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	vapi "github.com/hashicorp/vault/api"
)

// Auth methods.
const (
	AuthToken   = "token"
	AuthAppRole = "approle"
)

// Env variables claims reads (besides VAULT_ADDR / VAULT_CACERT, see cmd).
const (
	EnvToken    = "VAULT_TOKEN"
	EnvRoleID   = "VAULT_ROLE_ID"
	EnvSecretID = "VAULT_SECRET_ID"
)

// Options configure a client.
type Options struct {
	Addr         string
	CAFile       string
	AuthMethod   string // "" = token if one is found, else approle if VAULT_ROLE_ID is set
	Token        string // explicit token (--vault-token)
	AppRoleMount string // default "approle"
	RoleID       string // default $VAULT_ROLE_ID
	SecretID     string // default $VAULT_SECRET_ID
	Timeout      time.Duration
}

// Client is a logged-in Vault client.
type Client struct {
	Addr string
	// AuthSource says how the token was obtained (for doctor), never the token.
	AuthSource string
	c          *vapi.Client
}

// ErrNotFound is returned for a missing (or deleted) KV entry.
var ErrNotFound = errors.New("not found")

// ErrNotStrings is returned for an entry with values that are not strings.
var ErrNotStrings = errors.New("values are not strings")

// New builds a client for o.Addr and logs in.
func New(ctx context.Context, o Options) (*Client, error) {
	if o.Addr == "" {
		return nil, errors.New("no Vault address: configure the lab (claims secret vault add <lab> <addr>), or pass --vault-addr / set VAULT_ADDR")
	}
	cfg := vapi.DefaultConfig()
	if cfg.Error != nil {
		return nil, cfg.Error
	}
	cfg.Address = strings.TrimRight(o.Addr, "/")
	if o.Timeout > 0 {
		cfg.Timeout = o.Timeout
	}
	if o.CAFile != "" {
		if err := cfg.ConfigureTLS(&vapi.TLSConfig{CACert: o.CAFile}); err != nil {
			return nil, fmt.Errorf("vault CA file %s: %w", o.CAFile, err)
		}
	}
	// Retries hide a wrong address behind a long wait; claims fails fast.
	cfg.MaxRetries = 0
	vc, err := vapi.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	vc.ClearToken() // NewClient reads VAULT_TOKEN itself; claims decides below
	c := &Client{Addr: cfg.Address, c: vc}
	if err := c.login(ctx, o); err != nil {
		return nil, err
	}
	return c, nil
}

// NewWithToken builds a client with a fixed token and no login (tests).
func NewWithToken(addr, token string) (*Client, error) {
	return New(context.Background(), Options{Addr: addr, Token: token, AuthMethod: AuthToken})
}

func (c *Client) login(ctx context.Context, o Options) error {
	method := o.AuthMethod
	token, tokenSource := o.Token, "--vault-token"
	if token == "" {
		if t := os.Getenv(EnvToken); t != "" {
			token, tokenSource = t, "$"+EnvToken
		}
	}
	roleID, secretID := o.RoleID, o.SecretID
	if roleID == "" {
		roleID = os.Getenv(EnvRoleID)
	}
	if secretID == "" {
		secretID = os.Getenv(EnvSecretID)
	}
	if method == "" {
		switch {
		case token != "":
			method = AuthToken
		case roleID != "":
			method = AuthAppRole
		default:
			method = AuthToken
		}
	}
	switch method {
	case AuthToken:
		if token == "" {
			if t, err := tokenHelperFile(); err == nil && t != "" {
				token, tokenSource = t, "~/.vault-token"
			}
		}
		if token == "" {
			return fmt.Errorf("no Vault token for %s: pass --vault-token, set %s, run `vault login`, or use AppRole (%s/%s)", c.Addr, EnvToken, EnvRoleID, EnvSecretID)
		}
		c.c.SetToken(token)
		c.AuthSource = "token (" + tokenSource + ")"
		return nil
	case AuthAppRole:
		if roleID == "" || secretID == "" {
			return fmt.Errorf("AppRole login to %s needs %s and %s", c.Addr, EnvRoleID, EnvSecretID)
		}
		mount := o.AppRoleMount
		if mount == "" {
			mount = "approle"
		}
		s, err := c.c.Logical().WriteWithContext(ctx, "auth/"+strings.Trim(mount, "/")+"/login", map[string]any{
			"role_id": roleID, "secret_id": secretID,
		})
		if err != nil {
			return fmt.Errorf("AppRole login to %s (auth/%s): %w", c.Addr, mount, describe(err))
		}
		if s == nil || s.Auth == nil || s.Auth.ClientToken == "" {
			return fmt.Errorf("AppRole login to %s returned no token", c.Addr)
		}
		c.c.SetToken(s.Auth.ClientToken)
		c.AuthSource = "approle (auth/" + mount + ")"
		return nil
	default:
		return fmt.Errorf("vault auth method %q: claims supports %s and %s (OIDC is planned)", method, AuthToken, AuthAppRole)
	}
}

// tokenHelperFile reads ~/.vault-token, where `vault login` stores a token.
func tokenHelperFile() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(filepath.Join(home, ".vault-token"))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// describe shortens Vault response errors and adds hints for 403.
func describe(err error) error {
	var re *vapi.ResponseError
	if errors.As(err, &re) {
		msg := fmt.Sprintf("%d", re.StatusCode)
		if len(re.Errors) > 0 {
			msg += " " + strings.Join(re.Errors, "; ")
		}
		if re.StatusCode == http.StatusForbidden {
			msg += " (token expired, or its policies do not allow this path)"
		}
		return errors.New(msg)
	}
	return err
}

// TokenInfo is what doctor shows about the token.
type TokenInfo struct {
	DisplayName string
	Policies    []string
	TTL         time.Duration // 0 = no expiry
	Renewable   bool
}

// LookupSelf describes the client's token.
func (c *Client) LookupSelf(ctx context.Context) (*TokenInfo, error) {
	s, err := c.c.Auth().Token().LookupSelfWithContext(ctx)
	if err != nil {
		return nil, describe(err)
	}
	ti := &TokenInfo{}
	if s != nil && s.Data != nil {
		ti.DisplayName, _ = s.Data["display_name"].(string)
		ttl, _ := s.TokenTTL()
		ti.TTL = ttl
		ti.Renewable, _ = s.TokenIsRenewable()
		ti.Policies, _ = s.TokenPolicies()
	}
	return ti, nil
}

// Capabilities returns the token's capabilities on the KV v2 data path of
// mount/p (e.g. ["read","update"]).
func (c *Client) Capabilities(ctx context.Context, mount, p string) ([]string, error) {
	caps, err := c.c.Sys().CapabilitiesSelfWithContext(ctx, mount+"/data/"+p)
	if err != nil {
		return nil, describe(err)
	}
	return caps, nil
}

// Get reads the latest version of mount/p. Every value must be a string;
// others are reported by key (never by value).
func (c *Client) Get(ctx context.Context, mount, p string) (map[string]string, error) {
	s, err := c.c.KVv2(mount).Get(ctx, p)
	if errors.Is(err, vapi.ErrSecretNotFound) {
		return nil, fmt.Errorf("%s/%s: %w", mount, p, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s/%s: %w", mount, p, describe(err))
	}
	if s == nil || s.Data == nil {
		return nil, fmt.Errorf("%s/%s: %w (latest version deleted)", mount, p, ErrNotFound)
	}
	out := make(map[string]string, len(s.Data))
	var bad []string
	for k, v := range s.Data {
		str, ok := v.(string)
		if !ok {
			bad = append(bad, fmt.Sprintf("%s (%T)", k, v))
			continue
		}
		out[k] = str
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return nil, fmt.Errorf("%s/%s: %w (%s); a Secret holds strings only", mount, p, ErrNotStrings, strings.Join(bad, ", "))
	}
	return out, nil
}

// Put writes data as a new version of mount/p (replacing all keys) and
// reads it back; a mismatch is an error.
func (c *Client) Put(ctx context.Context, mount, p string, data map[string]string) error {
	in := make(map[string]any, len(data))
	for k, v := range data {
		in[k] = v
	}
	if _, err := c.c.KVv2(mount).Put(ctx, p, in); err != nil {
		return fmt.Errorf("writing %s/%s: %w", mount, p, describe(err))
	}
	back, err := c.Get(ctx, mount, p)
	if err != nil {
		return fmt.Errorf("read-back of %s/%s: %w", mount, p, err)
	}
	if len(back) != len(data) {
		return fmt.Errorf("read-back of %s/%s: %d keys written, %d read", mount, p, len(data), len(back))
	}
	for k, v := range data {
		if back[k] != v {
			return fmt.Errorf("read-back of %s/%s: key %s differs from what was written", mount, p, k)
		}
	}
	return nil
}

// List returns the secret paths below mount/prefix (recursively, relative to
// the mount, sorted). A missing prefix is an empty list.
func (c *Client) List(ctx context.Context, mount, prefix string) ([]string, error) {
	var out []string
	var walk func(dir string) error
	walk = func(dir string) error {
		lp := mount + "/metadata/" + dir
		s, err := c.c.Logical().ListWithContext(ctx, lp)
		if err != nil {
			return fmt.Errorf("listing %s/%s: %w", mount, dir, describe(err))
		}
		if s == nil || s.Data == nil {
			return nil
		}
		keys, _ := s.Data["keys"].([]any)
		for _, k := range keys {
			name, _ := k.(string)
			if name == "" {
				continue
			}
			if strings.HasSuffix(name, "/") {
				if err := walk(dir + name); err != nil {
					return err
				}
				continue
			}
			out = append(out, dir+name)
		}
		return nil
	}
	start := strings.Trim(prefix, "/")
	if start != "" {
		start += "/"
	}
	if err := walk(start); err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

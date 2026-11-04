package apiauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// ZitadelRolesClaim holds a user's project roles in ZITADEL tokens: an object
// whose keys are the role names. Project-specific variants
// (urn:zitadel:iam:org:project:<id>:roles) are read as well. Same claim as
// claim-machinery-api's AUTH_ROLES_CLAIM default.
const ZitadelRolesClaim = "urn:zitadel:iam:org:project:roles"

// Identity is what a token says about its holder, for `claims whoami`.
type Identity struct {
	Subject  string
	Username string
	Email    string
	Name     string
	Audience []string
	Expiry   time.Time
	Roles    []string
	Groups   []string
}

// UserRef is the Backstage-style user reference claim-machinery-api stamps
// into requested_by: user:default/<preferred_username before @>.
func (id Identity) UserRef() string {
	name := id.Username
	if name == "" {
		name = id.Email
	}
	if name == "" {
		return ""
	}
	if i := strings.Index(name, "@"); i > 0 {
		name = name[:i]
	}
	return "user:default/" + strings.ToLower(name)
}

// JWTClaims decodes the payload of a JWT without verifying it (display only;
// the API verifies). ok is false for opaque tokens.
func JWTClaims(raw string) (map[string]any, bool) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return nil, false
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, false
	}
	return claims, true
}

// IdentityFromClaims extracts the identity fields from token or userinfo claims.
func IdentityFromClaims(claims map[string]any) Identity {
	str := func(k string) string {
		s, _ := claims[k].(string)
		return s
	}
	id := Identity{
		Subject:  str("sub"),
		Username: str("preferred_username"),
		Email:    str("email"),
		Name:     str("name"),
		Audience: stringList(claims["aud"]),
		Groups:   stringList(claims["groups"]),
	}
	if exp, ok := claims["exp"].(float64); ok {
		id.Expiry = time.Unix(int64(exp), 0)
	}
	roles := map[string]bool{}
	for k, v := range claims {
		if k == ZitadelRolesClaim || (strings.HasPrefix(k, "urn:zitadel:iam:org:project:") && strings.HasSuffix(k, ":roles")) {
			for _, r := range stringList(v) {
				roles[r] = true
			}
		}
	}
	for r := range roles {
		id.Roles = append(id.Roles, r)
	}
	sort.Strings(id.Roles)
	sort.Strings(id.Groups)
	return id
}

// stringList reads a string, a list of strings, or an object whose keys are
// the values (ZITADEL roles).
func stringList(v any) []string {
	switch t := v.(type) {
	case string:
		if t == "" {
			return nil
		}
		return []string{t}
	case []any:
		var out []string
		for _, e := range t {
			if s, ok := e.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case map[string]any:
		out := make([]string, 0, len(t))
		for k := range t {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	return nil
}

// Userinfo calls the issuer's userinfo endpoint with the access token.
func Userinfo(ctx context.Context, hc *http.Client, endpoint, accessToken string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("userinfo: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("userinfo: HTTP %d", resp.StatusCode)
	}
	var claims map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&claims); err != nil {
		return nil, fmt.Errorf("userinfo: %w", err)
	}
	return claims, nil
}

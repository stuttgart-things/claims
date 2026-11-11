package gitops

import (
	"context"
	"errors"
	"fmt"
	"strings"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"
)

// TokenUser is the HTTPS user name sent with a token when no --git-user /
// GIT_USER is set; GitHub accepts any name together with a token.
const TokenUser = "x-access-token"

// IsSSHURL reports whether url is an SSH remote (git@host:o/r, ssh://...).
func IsSSHURL(url string) bool {
	if strings.HasPrefix(url, "ssh://") || strings.HasPrefix(url, "git+ssh://") {
		return true
	}
	if strings.Contains(url, "://") {
		return false
	}
	// scp-like: [user@]host:path, but not a Windows drive or a local path.
	at := strings.Index(url, "@")
	colon := strings.Index(url, ":")
	return at > 0 && colon > at && !strings.HasPrefix(url, "/")
}

// IsHTTPURL reports whether url is an http(s) remote.
func IsHTTPURL(url string) bool {
	return strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "http://")
}

// HTTPSURL turns an SSH remote into its HTTPS form (git@github.com:o/r.git ->
// https://github.com/o/r.git). claims authenticates with a token over HTTPS
// only (go-git has no access to the SSH agent here), so SSH remotes are
// pushed to and cloned from over HTTPS (claims#114). Other URLs are returned
// unchanged.
func HTTPSURL(url string) string {
	if !IsSSHURL(url) {
		return url
	}
	rest := url
	for _, p := range []string{"git+ssh://", "ssh://"} {
		rest = strings.TrimPrefix(rest, p)
	}
	if i := strings.Index(rest, "@"); i >= 0 {
		rest = rest[i+1:]
	}
	var host, path string
	if strings.HasPrefix(url, "ssh://") || strings.HasPrefix(url, "git+ssh://") {
		host, path, _ = strings.Cut(rest, "/")
		host, _, _ = strings.Cut(host, ":") // drop the SSH port
	} else {
		host, path, _ = strings.Cut(rest, ":")
	}
	return "https://" + host + "/" + strings.TrimPrefix(path, "/")
}

// transportFor is the URL and auth claims talks to a remote with: SSH remotes
// over HTTPS when there is a token; no auth for local paths.
func transportFor(url string, auth *http.BasicAuth) (string, transport.AuthMethod) {
	if auth == nil {
		return url, nil
	}
	if IsSSHURL(url) {
		return HTTPSURL(url), auth
	}
	if IsHTTPURL(url) {
		return url, auth
	}
	return url, nil // file:// or a path: no auth
}

func basicAuth(user, token string) *http.BasicAuth {
	if token == "" {
		return nil
	}
	if user == "" {
		user = TokenUser
	}
	return &http.BasicAuth{Username: user, Password: token}
}

// ProbeRemote lists the refs of url with token (git ls-remote): a cheap check
// that the remote is there and accepts the token, before anything is written.
func ProbeRemote(ctx context.Context, url, user, token string) error {
	target, auth := transportFor(url, basicAuth(user, token))
	r := git.NewRemote(memory.NewStorage(), &config.RemoteConfig{Name: "probe", URLs: []string{target}})
	if _, err := r.ListContext(ctx, &git.ListOptions{Auth: auth}); err != nil && !errors.Is(err, transport.ErrEmptyRemoteRepository) {
		return fmt.Errorf("git ls-remote %s: %w", target, err)
	}
	return nil
}

// BranchExists reports whether the local branch name exists.
func (g *GitOps) BranchExists(name string) bool {
	_, err := g.repo.Reference(plumbing.NewBranchReferenceName(name), false)
	return err == nil
}

// HasToken reports whether g pushes with a token.
func (g *GitOps) HasToken() bool { return g.auth != nil }

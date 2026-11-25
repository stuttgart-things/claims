package vault_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/stuttgart-things/claims/internal/vault"
	"github.com/stuttgart-things/claims/internal/vault/vaulttest"
)

func noVaultEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"VAULT_TOKEN", "VAULT_ROLE_ID", "VAULT_SECRET_ID", "VAULT_ADDR"} {
		t.Setenv(k, "")
	}
	t.Setenv("HOME", t.TempDir()) // no ~/.vault-token
}

func TestGetPutList(t *testing.T) {
	noVaultEnv(t)
	s := vaulttest.New(t)
	s.Put("kv/app/db", map[string]any{"user": "u1", "password": "p1"})
	s.Put("kv/app/nested/x", map[string]any{"k": "v"})
	s.Put("kv/other", map[string]any{"k": "v"})
	c, err := vault.NewWithToken(s.URL, s.Token)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	got, err := c.Get(ctx, "kv", "app/db")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, map[string]string{"user": "u1", "password": "p1"}) {
		t.Fatalf("Get = %v", got)
	}
	if _, err := c.Get(ctx, "kv", "missing"); !errors.Is(err, vault.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}

	list, err := c.List(ctx, "kv", "app")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(list, []string{"app/db", "app/nested/x"}) {
		t.Fatalf("List(app) = %v", list)
	}
	all, err := c.List(ctx, "kv", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("List() = %v", all)
	}
	if none, err := c.List(ctx, "kv", "nothing"); err != nil || len(none) != 0 {
		t.Fatalf("List(nothing) = %v, %v", none, err)
	}

	if err := c.Put(ctx, "kv", "app/new", map[string]string{"a": "b"}); err != nil {
		t.Fatal(err)
	}
	if s.Get("kv/app/new")["a"] != "b" || s.Writes() != 1 {
		t.Fatalf("Put did not store: %v", s.Get("kv/app/new"))
	}
}

func TestGetNonString(t *testing.T) {
	noVaultEnv(t)
	s := vaulttest.New(t)
	s.Put("kv/n", map[string]any{"port": 5432, "name": "x"})
	c, _ := vault.NewWithToken(s.URL, s.Token)
	_, err := c.Get(context.Background(), "kv", "n")
	if !errors.Is(err, vault.ErrNotStrings) || !strings.Contains(err.Error(), "port") {
		t.Fatalf("err = %v", err)
	}
}

func TestAuth(t *testing.T) {
	s := vaulttest.New(t)
	ctx := context.Background()

	t.Run("no credentials", func(t *testing.T) {
		noVaultEnv(t)
		if _, err := vault.New(ctx, vault.Options{Addr: s.URL}); err == nil || !strings.Contains(err.Error(), "no Vault token") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("token env", func(t *testing.T) {
		noVaultEnv(t)
		t.Setenv("VAULT_TOKEN", s.Token)
		c, err := vault.New(ctx, vault.Options{Addr: s.URL})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(c.AuthSource, "VAULT_TOKEN") {
			t.Fatalf("source = %s", c.AuthSource)
		}
		ti, err := c.LookupSelf(ctx)
		if err != nil || ti.TTL.Seconds() != 3600 {
			t.Fatalf("lookup = %+v, %v", ti, err)
		}
	})
	t.Run("approle", func(t *testing.T) {
		noVaultEnv(t)
		t.Setenv("VAULT_ROLE_ID", s.RoleID)
		t.Setenv("VAULT_SECRET_ID", s.SecretID)
		c, err := vault.New(ctx, vault.Options{Addr: s.URL})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(c.AuthSource, "approle") {
			t.Fatalf("source = %s", c.AuthSource)
		}
		caps, err := c.Capabilities(ctx, "kv", "app/db")
		if err != nil || len(caps) == 0 {
			t.Fatalf("caps = %v, %v", caps, err)
		}
	})
	t.Run("approle wrong secret", func(t *testing.T) {
		noVaultEnv(t)
		t.Setenv("VAULT_ROLE_ID", s.RoleID)
		t.Setenv("VAULT_SECRET_ID", "wrong")
		_, err := vault.New(ctx, vault.Options{Addr: s.URL})
		if err == nil || strings.Contains(err.Error(), "wrong") {
			t.Fatalf("err = %v (must not echo the secret id)", err)
		}
	})
	t.Run("bad token is 403", func(t *testing.T) {
		noVaultEnv(t)
		c, _ := vault.NewWithToken(s.URL, "nope")
		_, err := c.Get(ctx, "kv", "x")
		if err == nil || !strings.Contains(err.Error(), "403") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("no address", func(t *testing.T) {
		noVaultEnv(t)
		if _, err := vault.New(ctx, vault.Options{}); err == nil {
			t.Fatal("expected an error")
		}
	})
}

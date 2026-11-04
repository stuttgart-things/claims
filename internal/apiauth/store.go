package apiauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// EnvCredentialsFile overrides where logins are cached.
const EnvCredentialsFile = "CLAIMS_CREDENTIALS_FILE"

// Credential is one cached login, keyed by the API URL it is for. Each
// claim-machinery-api may sit behind a different ZITADEL (one per lab), so
// issuer and client are stored with the tokens.
type Credential struct {
	APIURL           string    `json:"apiURL"`
	Issuer           string    `json:"issuer"`
	ClientID         string    `json:"clientID"`
	Scope            string    `json:"scope,omitempty"`
	CAFile           string    `json:"caFile,omitempty"`
	TokenEndpoint    string    `json:"tokenEndpoint"`
	UserinfoEndpoint string    `json:"userinfoEndpoint,omitempty"`
	AccessToken      string    `json:"accessToken"`
	RefreshToken     string    `json:"refreshToken,omitempty"`
	TokenType        string    `json:"tokenType,omitempty"`
	Expiry           time.Time `json:"expiry,omitempty"`
	IDToken          string    `json:"idToken,omitempty"`
}

// LoginConfig returns the settings the credential was created with.
func (c Credential) LoginConfig() LoginConfig {
	return LoginConfig{Issuer: c.Issuer, ClientID: c.ClientID, Scope: c.Scope, CAFile: c.CAFile}
}

// Token returns the cached tokens as an oauth2.Token.
func (c Credential) Token() *oauth2.Token {
	return &oauth2.Token{
		AccessToken:  c.AccessToken,
		RefreshToken: c.RefreshToken,
		TokenType:    c.TokenType,
		Expiry:       c.Expiry,
	}
}

func (c *Credential) setToken(tok *oauth2.Token) {
	c.AccessToken = tok.AccessToken
	if tok.RefreshToken != "" {
		c.RefreshToken = tok.RefreshToken
	}
	c.TokenType = tok.TokenType
	c.Expiry = tok.Expiry
	if id, ok := tok.Extra("id_token").(string); ok && id != "" {
		c.IDToken = id
	}
}

// NormalizeAPIURL makes API URLs comparable (no surrounding space, no
// trailing slash).
func NormalizeAPIURL(u string) string {
	return strings.TrimRight(strings.TrimSpace(u), "/")
}

// Store is the credentials file (0600, directory 0700).
type Store struct {
	Path string
}

// DefaultStore returns $CLAIMS_CREDENTIALS_FILE, else
// <user config dir>/claims/credentials.json (~/.config/claims on Linux).
func DefaultStore() (*Store, error) {
	if p := os.Getenv(EnvCredentialsFile); p != "" {
		return &Store{Path: p}, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("locating the config directory: %w", err)
	}
	return &Store{Path: filepath.Join(dir, "claims", "credentials.json")}, nil
}

type storeFile struct {
	Credentials map[string]Credential `json:"credentials"`
}

func (s *Store) load() (storeFile, error) {
	f := storeFile{Credentials: map[string]Credential{}}
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, fmt.Errorf("reading %s: %w", s.Path, err)
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return f, fmt.Errorf("parsing %s: %w", s.Path, err)
	}
	if f.Credentials == nil {
		f.Credentials = map[string]Credential{}
	}
	return f, nil
}

func (s *Store) save(f storeFile) error {
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".credentials-*.json") // created 0600
	if err != nil {
		return fmt.Errorf("writing %s: %w", s.Path, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing %s: %w", s.Path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing %s: %w", s.Path, err)
	}
	if err := os.Rename(tmp.Name(), s.Path); err != nil {
		return fmt.Errorf("writing %s: %w", s.Path, err)
	}
	return nil
}

// Get returns the credential for apiURL.
func (s *Store) Get(apiURL string) (Credential, bool, error) {
	f, err := s.load()
	if err != nil {
		return Credential{}, false, err
	}
	c, ok := f.Credentials[NormalizeAPIURL(apiURL)]
	return c, ok, nil
}

// Put stores c under c.APIURL, replacing an earlier login for that URL.
func (s *Store) Put(c Credential) error {
	f, err := s.load()
	if err != nil {
		return err
	}
	c.APIURL = NormalizeAPIURL(c.APIURL)
	f.Credentials[c.APIURL] = c
	return s.save(f)
}

// Delete removes the login for apiURL and reports whether there was one.
func (s *Store) Delete(apiURL string) (bool, error) {
	f, err := s.load()
	if err != nil {
		return false, err
	}
	key := NormalizeAPIURL(apiURL)
	if _, ok := f.Credentials[key]; !ok {
		return false, nil
	}
	delete(f.Credentials, key)
	return true, s.save(f)
}

// URLs lists the API URLs with a cached login.
func (s *Store) URLs() ([]string, error) {
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	urls := make([]string, 0, len(f.Credentials))
	for u := range f.Credentials {
		urls = append(urls, u)
	}
	sort.Strings(urls)
	return urls, nil
}

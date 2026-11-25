package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/stuttgart-things/claims/internal/secretpath"
	"github.com/stuttgart-things/claims/internal/sops"
)

// claims doctor --secrets / --lab / --secret (claims#122): sops on PATH and
// new enough, an age key, the .sops.yaml rule for the target with the
// recovery recipient, whether the key can decrypt it, and the Vault login,
// token TTL and capabilities.

// EnvRecoveryRecipient names the offline recovery recipient every rule must carry.
const EnvRecoveryRecipient = "CLAIMS_SOPS_RECOVERY_RECIPIENT"

var doctorSecretFlags struct {
	enabled  bool
	secret   string
	recovery string
	noVault  bool
}

// tokenTTLWarn is the TTL below which doctor warns.
const tokenTTLWarn = 15 * time.Minute

func init() {
	f := doctorCmd.Flags()
	f.BoolVar(&doctorSecretFlags.enabled, "secrets", false, "Also check sops, the age key, .sops.yaml and Vault (implied by --lab/--secret)")
	f.StringVar(&secretOpts.lab, "lab", "", "Lab to check the secret setup for (.sops.yaml rule, Vault)")
	f.StringVar(&doctorSecretFlags.secret, "secret", "", "SOPS file or Vault path (with --lab) to check the rule, decryption and Vault capabilities for")
	f.StringVar(&doctorSecretFlags.recovery, "recovery-recipient", "", "Age recipient every rule must carry (default: $"+EnvRecoveryRecipient+", else the recipient shared by all lab rules)")
	f.BoolVar(&doctorSecretFlags.noVault, "no-vault", false, "Skip the Vault checks")
	f.StringVar(&secretOpts.vaultAddr, "vault-addr", "", "Vault address (default: the lab's Vault from the config, then $VAULT_ADDR)")
	f.StringVar(&secretOpts.vaultToken, "vault-token", "", "Vault token (default: $VAULT_TOKEN, AppRole env, ~/.vault-token)")
	f.StringVar(&secretOpts.vaultCAFile, "vault-ca-file", "", "PEM file with the CA of the Vault")
	doctorCmd.Long += `

With --secrets, --lab or --secret also (claims secret):
  sops           on PATH, version >= 3.9 (--filename-override)
  age key        SOPS_AGE_KEY / SOPS_AGE_KEY_FILE / SOPS_AGE_KEY_CMD / the default key file
  .sops.yaml     the repository root has a creation rule for the target
                 (--secret, else secrets/<lab>/...), with the recovery recipient
  age decrypt    your key is one of the rule's recipients
  vault          login to the lab's Vault, token TTL, capabilities on --secret`
	doctorCmd.Example += `
  claims doctor --no-api --no-git --lab labul
  claims doctor --no-api --no-git --secret secrets/labul/kv/app/db.enc.yaml`
}

func doctorSecretsWanted() bool {
	return doctorSecretFlags.enabled || secretOpts.lab != "" || doctorSecretFlags.secret != ""
}

// checkSecrets runs the secret checks against the checkout dir.
func checkSecrets(ctx context.Context, r *preflightReport, dir string) {
	if v, err := sops.Version(ctx); err != nil {
		r.fail("sops", err.Error(), "install sops >= 3.9: https://github.com/getsops/sops/releases")
	} else if !sops.VersionAtLeast(v, sops.MinVersion) {
		r.fail("sops", "sops "+v+" is too old (needs --filename-override, 3.9+)", "install sops >= 3.9")
	} else {
		r.ok("sops", "sops "+v)
	}

	keys, keyErr := sops.FindAgeKeys()
	switch {
	case keyErr != nil:
		r.fail("age key", keyErr.Error(), "export SOPS_AGE_KEY_FILE=<your age key file>")
	case keys.Opaque:
		r.ok("age key", keys.Source+" (not inspected)")
	default:
		r.ok("age key", fmt.Sprintf("%s from %s", plural(len(keys.Recipients), "age key"), keys.Source))
	}

	root, err := findRepoRoot(dir)
	if err != nil {
		r.warn(".sops.yaml", fmt.Sprintf("%s is not in a git checkout: .sops.yaml checks skipped (use --dir)", dir))
		return
	}
	secretOpts.repo = root

	// The target: --secret, else a probe path of the lab.
	var t secretTarget
	haveTarget := false
	if doctorSecretFlags.secret != "" {
		if t, err = resolveSecretTarget(root, doctorSecretFlags.secret); err != nil {
			r.fail("secret", err.Error(), "")
			return
		}
		haveTarget = true
	} else if lab, err := secretLab(); err == nil {
		if err := secretpath.ValidLab(lab); err != nil {
			r.fail("lab", err.Error(), "")
			return
		}
		t = targetFor(root, secretpath.Ref{Lab: lab, Mount: "kv", Path: "claims-doctor-probe"})
	} else {
		r.warn(".sops.yaml", "no --lab or --secret: rule checks skipped")
		return
	}

	cfg, err := sops.LoadConfig(root)
	if err != nil {
		r.fail(".sops.yaml", err.Error(), "add the root .sops.yaml of docs/13-secrets.md to the fleet repository")
		return
	}
	rule, err := cfg.RuleFor(t.Rel)
	if err != nil {
		r.fail(".sops.yaml", err.Error(), "add a rule path_regex: ^secrets/"+t.Ref.Lab+"/.*\\.enc\\.yaml$ (docs/13-secrets.md)")
		return
	}
	r.ok(".sops.yaml", fmt.Sprintf("rule %d (%s) for %s: %s", rule.Index, rule.PathRegex, t.Rel, plural(len(rule.Age), "recipient")))
	if rule.EncryptedRegex != "^(data|stringData)$" {
		r.warn(".sops.yaml", fmt.Sprintf("rule %d has encrypted_regex %q, expected ^(data|stringData)$", rule.Index, rule.EncryptedRegex))
	}
	checkRecovery(r, cfg, rule)

	if keyErr == nil {
		ok, known := keys.CanDecrypt(rule.Age)
		switch {
		case !known:
			r.warn("age decrypt", "SOPS_AGE_KEY_CMD: cannot tell whether your key is a recipient")
		case ok:
			r.ok("age decrypt", "your key is a recipient of "+t.Rel)
		default:
			r.fail("age decrypt", "your age key is not a recipient of the rule for "+t.Rel, "use the lab's key (SOPS_AGE_KEY_FILE), or ask for your recipient to be added")
		}
	}
	if haveTarget && t.exists() {
		if enc, err := os.ReadFile(t.Abs); err == nil {
			file := recipientSet(enc)
			want := map[string]bool{}
			for _, a := range rule.Age {
				want[a] = true
			}
			if d := recipientDelta(file, want); d != "" {
				r.warn("recipients", fmt.Sprintf("%s is encrypted for other recipients than .sops.yaml names (%s): claims secret sops rotate %s", t.Rel, d, t.Rel))
			} else {
				r.ok("recipients", t.Rel+" matches .sops.yaml")
			}
		}
	}

	if doctorSecretFlags.noVault {
		return
	}
	checkVault(ctx, r, t, haveTarget)
}

// checkRecovery checks that the rule carries the offline recovery recipient.
func checkRecovery(r *preflightReport, cfg *sops.Config, rule *sops.Rule) {
	rec := doctorSecretFlags.recovery
	if rec == "" {
		rec = os.Getenv(EnvRecoveryRecipient)
	}
	if rec != "" {
		if rule.Has(rec) {
			r.ok("recovery", "rule carries the recovery recipient "+sops.ShortRecipient(rec))
		} else {
			r.fail("recovery", fmt.Sprintf("rule %d lacks the recovery recipient %s", rule.Index, sops.ShortRecipient(rec)), "add it to the rule's age: list, then claims secret sops rotate secrets/")
		}
		return
	}
	if shared := cfg.SharedRecipients(); len(shared) > 0 {
		var s []string
		for _, a := range shared {
			s = append(s, sops.ShortRecipient(a))
		}
		r.ok("recovery", "shared by every lab rule: "+strings.Join(s, ", "))
		return
	}
	if len(rule.Age) < 2 {
		r.fail("recovery", fmt.Sprintf("rule %d has a single recipient: no offline recovery recipient", rule.Index), "add the recovery recipient to every rule (docs/13-secrets.md)")
		return
	}
	r.warn("recovery", "cannot tell which recipient is the recovery one: set --recovery-recipient or $"+EnvRecoveryRecipient)
}

// checkVault logs in to the lab's Vault and checks the token.
func checkVault(ctx context.Context, r *preflightReport, t secretTarget, haveTarget bool) {
	pctx, cancel := context.WithTimeout(ctx, 2*preflightTimeout)
	defer cancel()
	vc, err := newVaultClient(pctx, t.Ref.Lab)
	if err != nil {
		r.fail("vault", err.Error(), "claims secret vault add "+t.Ref.Lab+" <addr>; VAULT_TOKEN or VAULT_ROLE_ID/VAULT_SECRET_ID")
		return
	}
	ti, err := vc.LookupSelf(pctx)
	if err != nil {
		r.fail("vault", fmt.Sprintf("%s: token lookup failed: %v", vc.Addr, err), "log in again (vault login / a fresh VAULT_TOKEN / AppRole)")
		return
	}
	ttl := "no expiry"
	if ti.TTL > 0 {
		ttl = "ttl " + ti.TTL.Round(time.Second).String()
	}
	detail := fmt.Sprintf("%s, %s, %s", vc.Addr, vc.AuthSource, ttl)
	if len(ti.Policies) > 0 {
		detail += ", policies " + strings.Join(ti.Policies, ",")
	}
	if ti.TTL > 0 && ti.TTL < tokenTTLWarn {
		r.warn("vault", detail+" (expires soon)")
	} else {
		r.ok("vault", detail)
	}
	if !haveTarget {
		return
	}
	caps, err := vc.Capabilities(pctx, t.Ref.Mount, t.Ref.Path)
	if err != nil {
		r.fail("vault caps", err.Error(), "")
		return
	}
	has := map[string]bool{}
	for _, c := range caps {
		has[c] = true
	}
	switch {
	case has["root"]:
		r.ok("vault caps", t.Ref.VaultPath()+": root")
	case has["deny"] || !has["read"]:
		r.fail("vault caps", fmt.Sprintf("%s: %s (diff and sync need read)", t.Ref.VaultPath(), strings.Join(caps, ",")), "grant read on "+t.Ref.Mount+"/data/"+t.Ref.Path+" and list on "+t.Ref.Mount+"/metadata/*")
	default:
		r.ok("vault caps", t.Ref.VaultPath()+": "+strings.Join(caps, ","))
	}
}

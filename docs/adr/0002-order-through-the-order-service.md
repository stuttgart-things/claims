# 0002: Order through machinery-order-service, without a git token

- Status: accepted
- Date: 2026-11-18

## Context

`claims render --create-pr` writes the order into the fleet repository itself: it pushes a branch and opens the PR with the user's GitHub token. Everyone who orders therefore needs write access to the fleet repository, which is wrong for people (and personas) who should only order.

machinery-order-service already takes orders over REST (`POST /api/v1/orders`): it renders with the orderer's token, opens the PR with its own GitHub credentials and follows the order through merge and build (`PROpen`, `Merged`, `Building`, `Ready`). It authenticates against the same ZITADEL project as claim-machinery-api, so the claims login is valid there. The other server path (claim-machinery-api `order-pr`) is switched off on both machinery clusters.

## Decision

- `claims order <template>` orders through machinery-order-service: same parameters as `render` (`-p`, `-f`, form), plus `--cluster` (management cluster) and `--reason`. It prints the order ID, its phase and how to follow it; it never touches git and needs no git token.
- `claims orders` follows orders: `list` (own and own teams' by default), `get <id>`, `watch <id>` (until a final phase), `cancel <id>`. `claims clusters` lists the management clusters.
- The order service URL belongs to the endpoint, as `orders-url` in `~/.config/claims/config.yaml` (`claims endpoint add … --orders-url`), overridable by `--orders-url` / `CLAIM_ORDERS_URL`. Without one, the order commands fail with the fix in the message.
- `render --create-pr` stays for people who do have repository access and want the PR from their own account.
- Both faces of ADR 0001 apply: interactive picks template, cluster and parameters; programmatic takes flags and `-o json`.

## Consequences

- One login orders through the platform; the service's GitHub identity opens the PRs, and the order names the requester.
- claims depends on the order service's REST API; its contract (fields, phases, errors) is versioned there.
- Implements #124; the template list is #125.

# Dedicated Surge Snell

This fork supports a dedicated Snell mode for Surge users. It preserves the
normal multi-user Snell behaviour and activates dedicated mode only when both
conditions are true:

- the inbound type is `snell`;
- the inbound tag starts with `SurgeSnell-` (case-sensitive).

## Create a node

1. Create the Client account first and set its Volume, Expiry, Enable and Auto
   Reset fields as needed.
2. Create a Snell inbound whose tag is `SurgeSnell-<account>`.
3. Select exactly one Client in the inbound's initial users field. A Client can
   own at most one dedicated Surge Snell inbound, and an inbound cannot be
   shared by multiple Clients.
4. Set the Snell port, version and `psk`. The inbound's **pre-shared key is the
   PSK used by Surge**.

The Client's QR dialog, under **Links**, then contains a complete entry for the
Surge `[Proxy]` section, for example:

```ini
SurgeSnell-Alice = snell, 203.0.113.10, 30660, psk=example-key, version=5, reuse=true
```

The generated entry tracks edits to the inbound tag, public address, port,
PSK, version, v5 HTTP obfuscation and v6 mode. S-UI's raw subscription output
uses the same line (and may base64-encode the complete response when the global
subscription encoding option is enabled).

## Accounting and lifecycle

Surge authenticates Snell with a shared PSK and does not send sing-box's
per-user `userkey`. Dedicated mode therefore keeps the live Snell config
PSK-only and attributes the inbound counters to its sole Client. Existing
Client behaviour then applies without a database migration:

- upload/download usage and traffic charts;
- Volume quota and Expiry enforcement;
- manual Enable/Disable;
- periodic Auto Reset and global traffic reset.

When the Client is disabled, expired, over quota or temporarily unassigned,
the dedicated listener is removed from the running core. It is restored when
the Client becomes active again. Ambiguous legacy data with multiple owners
fails closed.

Do not share a dedicated inbound's PSK: every device using that PSK contributes
to the same Client's traffic counters.

## Rollback

The feature adds no database columns or migrations. Reverting its Git commit
restores stock behaviour without converting the database:

```sh
git revert <surge-snell-feature-commit>
```

If using the standalone patch bundle published with this fork, run its
`rollback.sh` from a clean checkout. Dedicated inbounds remain valid ordinary
Snell records after rollback, but Surge-specific per-Client accounting and
generated Links are no longer available.

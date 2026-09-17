# Standalone Surge Snell patch

This bundle contains the dedicated Surge Snell feature from commit `52fbaf7`.
It is intended for a clean S-UI v1.6.3 source tree at commit `13abbdc`.

Patch SHA-256:

```text
1e5fc099553bbfbaf8c7605bbea04ae0b1ca4c87cd5fc3389c24a4c14c38277a  surge-snell.patch
```

Apply it to another checkout:

```sh
./contrib/surge-snell/apply.sh /path/to/s-ui
```

Reverse only the changes in this patch:

```sh
./contrib/surge-snell/rollback.sh /path/to/s-ui
```

Both scripts run `git apply --check` before changing files. They do not alter
the database, create commits, restart a service or replace a deployed binary.
Commit and deploy the resulting source changes through your normal workflow.

The reverse operation intentionally leaves this bundle itself in place so it
can be audited or applied again.

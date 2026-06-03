# AGENTS.md — rules the JSON Schema can't express

`manifest.v0.3.schema.json` catches field-level errors (wrong type,
unknown enum value, malformed regex). The rules below are
**semantic** — cross-field invariants, runtime resolution rules,
and naming conventions that grove's manifest validator enforces
at parse time. LLMs and humans writing manifests should treat
them as if they were schema constraints.

When grove rejects a manifest with one of these rules, the error
message names the rule (e.g. "rule-4: volume role/backup
coupling"). Search this file for the rule number to find a
concrete fix.

---

## Rule 1 — Interpolation scope

The three interpolation forms (`${secret.NAME}`, `${user.NAME}`,
`${grove.public_url}`) are resolved ONLY in these fields:

- `services[].env` values
- `services[].hooks.*.cmd` arguments

In every other field — including `services[].args`,
`services[].command`, image strings, paths, port names — the
sequence `${...}` is passed through to the container verbatim
without substitution.

✓ correct
```json
"env": {
  "DATABASE_URL": "postgres://app:${secret.PG_PASS}@db:5432/app"
}
```

✗ silently wrong (passes schema, fails at runtime)
```json
"args": ["--password=${secret.PG_PASS}"]
```

If a flag must receive a secret, read it from an env var inside
the container's entrypoint:
```json
"env": { "PG_PASS": "${secret.PG_PASS}" },
"args": ["--password-from-env=PG_PASS"]
```

---

## Rule 2 — Public port requirement

At least one service in `services[]` must declare one port with
`public: true`. Without this, the app boots but is unreachable
externally (no port-forward on local, no reverse-proxy route on
cloud).

✓ correct: one of N services has a public port
```json
{"name":"app","ports":[{"name":"http","container":8000,"public":true}]}
```

✗ rejected at validation
```json
"services": [
  {"name":"app","ports":[{"name":"http","container":8000}]}
]
```
(no port has `public: true`)

Sidecar services (postgres, redis, clickhouse) do NOT need
public ports — services reach each other on any port via the
private network.

---

## Rule 3 — Reference validation

Every interpolation `${X.NAME}` must point to a declared entry:

- `${secret.NAME}` → must exist in top-level `secrets[].name`
- `${user.NAME}` → must exist in top-level `user_inputs[].name`
- `${grove.public_url}` and `${grove.old_public_url}` → fixed
  names, no declaration needed

Unknown references are validation errors, not silent
empty-string substitutions.

The same rule applies to direct field references:

- `backup.pg_dump.password_secret` → must exist in `secrets[]`
- (any future `secret_ref` style fields) → same

✓ correct
```json
"secrets": [{"name":"PG_PASS","generate":"random:32"}],
"services": [{
  "env": {"DB_PASS": "${secret.PG_PASS}"}
}]
```

✗ rejected
```json
"secrets": [{"name":"PG_PASS","generate":"random:32"}],
"services": [{
  "env": {"DB_PASS": "${secret.POSTGRES_PASSWORD}"}
}]
```
(POSTGRES_PASSWORD not declared)

---

## Rule 4 — Volume role ↔ backup coupling

The `volume.role` field determines whether `backup` must be
present:

| role | meaning | backup required? |
|---|---|---|
| `state` (default) | authoritative data, included in deploy snapshot | YES |
| `cache` | persists across restart, regenerated on the cloud side after fork | NO (forbidden) |
| `ephemeral` | tmpfs, does not survive restart | NO (forbidden) |

✓ correct
```json
{"name":"pg_data","role":"state","backup":{"driver":"pg_dump","dbname":"app"}}
{"name":"thumbnails","role":"cache"}
{"name":"tmp","role":"ephemeral"}
```

✗ rejected (state without backup)
```json
{"name":"pg_data","role":"state"}
```

✗ rejected (cache with backup)
```json
{"name":"thumbnails","role":"cache","backup":{"driver":"filesystem"}}
```

This rule exists because "omit `backup` to opt out" is an
ambiguous signal — the validator can't tell whether the author
forgot or intentionally excluded. Explicit `role` disambiguates.

---

## Rule 5 — `init_chown` is chown, not chmod, and UID/GID ≥ 1

Despite the legacy "init_chmod" mental model, the field is
**chown** — it sets ownership (UID:GID), not file mode bits.

- Format: `"<uid>:<gid>"` where both are integers ≥ 1
  (assigning 0:0 is rejected; root ownership on a host bind
  mount becomes unrecoverable by the user)
- Runs once on first volume creation; subsequent runs detect
  the sentinel file `.grove-init` and skip
- Recursive: applies to all existing contents

✓ correct
```json
"volumes": [{"name":"data","path":"/var/lib/x","role":"state",
             "init_chown":"999:999","backup":{...}}]
```

✗ rejected (chmod-style octal)
```json
"init_chown": "0755"
```

✗ rejected (root)
```json
"init_chown": "0:0"
```

---

## Rule 6 — `depends_on` must form a DAG

`services[].depends_on` declares "wait until these are healthy
before starting." Cycles are rejected at validation.

✓ correct
```
postgres ── (no deps)
clickhouse ── (no deps)
app ── depends_on [postgres, clickhouse]
```

✗ rejected
```
a ── depends_on [b]
b ── depends_on [a]
```

There is no escape hatch (no soft-dependency, no
ignore-on-cycle). If two services must start together, model
the relationship differently — e.g. merge into one
service, or move the coordination logic to one side's
`hooks.pre_upgrade` / startup script.

---

## Rule 7 — Name uniqueness

Within a single manifest:

- `services[].name` must be unique (it's used as DNS hostname
  between services)
- `volumes[].name` must be unique within each service (it's
  used as the on-disk volume identifier scoped to that service)
- `secrets[].name` must be unique (UPPER_SNAKE_CASE; referenced
  by `${secret.X}`)
- `user_inputs[].name` must be unique
- `ports[].name` must be unique within each service (referenced
  by `health.http.port_name`)

JSON Schema can't express these (it allows duplicate keys in
arrays of objects), but grove rejects duplicates with a
specific error pointing at the conflicting names.

---

## Rule 8 — `${grove.old_public_url}` scope

The variable `${grove.old_public_url}` represents the URL of
the SOURCE instance that a fork-deploy snapshot was taken from.
It is meaningful only during the `post_restore` hook on the
cloud side, and is therefore valid ONLY in:

- `services[].hooks.post_restore[].cmd` arguments

Anywhere else — including service `env`, other hook types,
`backup.exec.snapshot/restore` — `${grove.old_public_url}` is
either undefined or passes through as a literal string. Grove
rejects manifests that reference it outside the allowed scope.

The other grove variable, `${grove.public_url}`, is always
available (in env values and any hook cmd).

✓ correct
```json
"hooks": {
  "post_restore": [{
    "cmd": ["sed","-i","s|${grove.old_public_url}|${grove.public_url}|g","/data/config.json"]
  }]
}
```

✗ rejected
```json
"env": {"OLD_URL": "${grove.old_public_url}"}
```

---

## Rule 9 — Secret refs inside backup blocks

The `backup` $defs hold their own secret references that follow
the same rules as `${secret.NAME}` interpolation but live in
typed fields rather than string interpolation:

- `backup.pg_dump.password_secret` → secret name in `secrets[]`
- `backup.exec.snapshot[]` and `backup.exec.restore[]` strings
  → may contain `${secret.X}` and `${grove.X}` interpolation,
  same scope rules as Rule 1

Grove validates these references at manifest parse time, with
the same "rejected if missing" semantics as Rule 3.

✓ correct
```json
"secrets": [{"name":"PG_PASS","generate":"random:32"}],
"backup": {
  "driver": "pg_dump",
  "dbname": "app",
  "password_secret": "PG_PASS"
}
```

✗ rejected
```json
"secrets": [{"name":"PG_PASS","generate":"random:32"}],
"backup": {
  "driver": "pg_dump",
  "dbname": "app",
  "password_secret": "POSTGRES_PASSWORD"
}
```

---

## When in doubt

Look at `example-plausible.v0.3.json` for the maximalist
manifest (multi-container, all interpolation forms, hooks,
user_inputs). Look at `example-linkding.v0.3.json` for a
typical single-container app with secrets. Look at
`example-memos.v0.3.json` for the absolute minimum — just one
container, one volume, one port.

If a rule above is unclear, the schema's `description` field
on the relevant property is the canonical source. This file
explains the **cross-field** consequences that descriptions on
individual fields can't.

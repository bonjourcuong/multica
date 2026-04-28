# Deploy — Multica Fork (`team.cuongpho.com`)

The Multica Fork ships from `main` to `team.cuongpho.com`. Builds and deploys
are agent-prepared and **human-executed**: agents (JARVIS) build images,
print the plan, and stop. Cuong runs the actual `docker compose up -d` from
his own SSH session.

This document describes that two-step sequence and the supporting script
`scripts/deploy-fork-build.sh`.

## Hard rule

**No agent runs `docker compose up` against `team.cuongpho.com`.** Every
deploy is initiated by a human. Any automation that violates this is a
release blocker, not a feature. See ADR `docs/adrs/2026-04-28-global-orchestrator-chat.md`
(decision D8).

## What `scripts/deploy-fork-build.sh` does

It is a **dry-run + plan-only** preflight. Running it never touches a running
container or applies a migration. Steps:

1. Pre-flight: verify `docker`, `docker compose`, the compose files, and the
   `king-postgres` container.
2. `git pull --ff-only` on the local checkout (refused unless on `main`).
3. `docker compose -f docker-compose.selfhost.yml -f docker-compose.selfhost.build.yml build`
   to build the fork's `multica-backend:dev` and `multica-web:dev` images.
4. `docker compose -f docker-compose.selfhost.yml -f docker-compose.selfhost.build.yml config`
   to print the effective merged compose config.
5. **Migration dry-run**: connect read-only into the prod `king-postgres`
   container, read `schema_migrations`, list the pending migration files
   from `server/migrations/`, and print the SQL that the backend's
   entrypoint will run on next start. Nothing is applied.
6. Print the two commands Cuong runs next, the healthcheck, and the
   rollback path.

Migrations are applied automatically by the backend container's entrypoint
(`docker/entrypoint.sh` calls `./migrate up` before `./server`). The dry-run
exists so you can review the SQL before it lands.

## Operator-tunable knobs

The script reads the following environment variables, all optional:

| Variable | Default | Purpose |
| --- | --- | --- |
| `MULTICA_FORK_DIR` | resolved from script path (canonical: `/root/multica-fork`) | fork checkout |
| `COMPOSE_BASE` | `docker-compose.selfhost.yml` | base compose file |
| `COMPOSE_BUILD_OVERRIDE` | `docker-compose.selfhost.build.yml` | local-build override |
| `POSTGRES_CONTAINER` | `king-postgres` | running prod DB container |
| `HEALTHCHECK_URL` | `https://api-team.cuongpho.com/healthz` | post-deploy probe |
| `ENV_FILE` | `.env` | sourced for `POSTGRES_USER` / `POSTGRES_DB` |
| `MIGRATIONS_DIR` | `server/migrations` | dry-run source |

## The deploy sequence (Cuong)

All commands run on `terminator-9999` from a personal SSH session.

### 1. Dry-run

```bash
ssh terminator-9999
cd /root/multica-fork
./scripts/deploy-fork-build.sh 2>&1 | tee -a deploy-$(date -u +%Y%m%dT%H%M%SZ).log
```

Read the printed plan. Confirm:

- The HEAD commit shown matches what you expect to ship.
- The compose `config` lists `multica-backend:dev` and `multica-web:dev` as
  the images about to run.
- The pending migrations list matches the diff from the merged feature PRs.
  If a migration looks wrong, abort and post on the master issue.

### 2. Apply

The backend entrypoint runs `./migrate up` before booting `./server`, so a
single `up -d` covers both schema and runtime:

```bash
cd /root/multica-fork
docker compose -f docker-compose.selfhost.yml -f docker-compose.selfhost.build.yml up -d
```

Wait roughly 30 seconds for migration + boot, then verify:

```bash
docker ps --filter name=multica
curl -sS https://api-team.cuongpho.com/healthz && echo
```

A healthy `/healthz` is the green light. If the curl fails or the backend
container restarts in a loop, jump to rollback.

## Rollback

The fork stack and the upstream stack share the same `docker-compose.selfhost.yml`
service definitions; only the override file flips images from upstream GHCR
to local `*:dev` builds. To revert:

```bash
cd /root/multica-fork
docker compose -f docker-compose.selfhost.yml up -d
```

This pulls the published GHCR image (per `MULTICA_IMAGE_TAG` in `.env`) and
replaces the running fork containers. The backend entrypoint will run
migrations again — note that **down migrations are not run** on rollback,
so any schema change introduced by the failed deploy stays applied. If a
schema rollback is needed, file an incident and fix forward.

## Backups

Before any production deploy that ships a non-trivial migration, snapshot
`king-postgres` first:

```bash
docker exec -t king-postgres pg_dumpall -U "$POSTGRES_USER" \
  | gzip > /root/backups/king-postgres-$(date -u +%Y%m%dT%H%M%SZ).sql.gz
```

Keep a minimum of 7 daily backups before considering pruning.

## Where to log the deploy

After `up -d` completes and the healthcheck is green, post a comment on the
master issue with: HEAD commit, list of pending → applied migrations, and a
timestamp. The deploy log file from step 1 lives next to the checkout for
forensic reference.

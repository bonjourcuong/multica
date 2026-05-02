# Image pinning runbook

The fork's compose stack pins external images by digest and the locally-built
backend/web images by commit SHA. This document is the operator's reference
for keeping those pins current.

## What is pinned, and where

| Image | Pin location | Format |
|---|---|---|
| `pgvector/pgvector` | `docker-compose.selfhost.yml` | tag + `@sha256:...` digest |
| `multica-backend-fork` | `.env` `MULTICA_IMAGE_TAG` (= short commit SHA) | tag only |
| `multica-web-fork` | `.env` `MULTICA_IMAGE_TAG` (same value) | tag only |

The two fork images share `MULTICA_IMAGE_TAG` because they always ship together.
`scripts/deploy-fork-build.sh` exports `MULTICA_IMAGE_TAG=$(git rev-parse --short HEAD)`
before the build, so the freshly built image is named after the SHA being
deployed (no manual `docker tag` step needed).

## Rotating the pgvector digest

Run when a new pgvector PG17 release lands and you want it.

1. **Look up the current upstream digest** for the tag you want (no need to
   pull yet — `docker manifest inspect` is enough):

   ```bash
   docker manifest inspect pgvector/pgvector:pg17 \
     | jq -r '.manifests[]? | select(.platform.architecture=="amd64") | .digest' \
     | head -n1
   ```

   If `manifests` is missing (single-arch image), use:

   ```bash
   docker manifest inspect pgvector/pgvector:pg17 | jq -r '.config.digest // .digest'
   ```

2. **Pull the new digest locally** and verify it boots against a scratch
   volume before touching prod:

   ```bash
   docker pull pgvector/pgvector:pg17@sha256:<new-digest>
   docker run --rm -e POSTGRES_PASSWORD=test \
     pgvector/pgvector:pg17@sha256:<new-digest> postgres --version
   ```

3. **Update the pin** in `docker-compose.selfhost.yml`:

   ```yaml
   image: pgvector/pgvector:pg17@sha256:<new-digest>
   ```

4. **Open a PR** with the bump. Mention the upstream changelog entry in the
   commit body; pgvector is in the data path and any extension change can
   affect query plans for embedding indexes.

5. **Roll out** through the normal `scripts/deploy-fork-build.sh` flow. The
   migration entrypoint runs first; if it fails, roll back by reverting the
   PR and redeploying — the old digest is still cached locally.

## Rolling forward MULTICA_IMAGE_TAG

This happens automatically on every deploy: `deploy-fork-build.sh` rev-parses
HEAD and exports the short SHA before building. Do not edit `.env`'s
`MULTICA_IMAGE_TAG` by hand for a normal deploy — the export overrides it.

The value in `.env` is the **fallback for `up -d` without a fresh build**, e.g.
when bringing the stack back up after a host reboot. Keep it in sync with the
last successfully deployed SHA so a bare `docker compose up -d` resurrects the
right image.

After every successful deploy:

```bash
sed -i "s/^MULTICA_IMAGE_TAG=.*/MULTICA_IMAGE_TAG=$(git -C /root/multica rev-parse --short HEAD)/" /root/multica/.env
```

(`scripts/deploy-fork-build.sh` does not write to `.env` itself — keeping
the script side-effect-free on host config — but the post-deploy sweep
should run the line above and verify with `docker ps` that every
`multica-*` container shows `(healthy)`.)

## Rollback

The previous SHA is still tagged locally as long as `docker image prune` has
not run. To roll back:

```bash
docker images multica-backend-fork --format 'table {{.Tag}}\t{{.CreatedAt}}'
MULTICA_IMAGE_TAG=<previous-sha> docker compose \
  -f /root/multica/docker-compose.selfhost.yml \
  -f /root/multica/docker-compose.production.yml \
  up -d
```

For pgvector: revert the digest in `docker-compose.selfhost.yml` and redeploy.
The previous digest is still cached locally if it was pulled within the
typical Docker GC window.

## Why we pin

- **pgvector by digest**: registry tags are mutable. Without a digest pin, an
  upstream `:pg17` retag could swap the running image during the next pull,
  potentially with a breaking pgvector extension upgrade. The digest makes
  the pull deterministic.
- **Fork images by SHA**: `:latest` and `:dev` tags hide which commit is in
  production. SHA tags make the running container traceable to a single git
  revision in one `docker ps` glance.

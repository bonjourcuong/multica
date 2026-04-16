-- Stabilize daemon_id as a persistent UUID.
--
-- Before this change daemon_id was derived from os.Hostname(), so changes
-- like the macOS `.local` suffix appearing/disappearing, the user renaming
-- their machine, or switching profiles produced a fresh agent_runtime row
-- every time, stranding agents on the stale one.
--
-- From this migration forward the daemon generates a UUID on first start,
-- writes it to ~/.multica/<profile>/daemon.id, and re-uses it forever. The
-- server-side DaemonRegister flow consolidates any pre-existing rows that
-- match the historic hostname-based daemon_id candidates (hostname,
-- hostname.local, hostname-<profile>, hostname.local-<profile>) into the
-- new UUID row, so agents keep pointing to the same runtime id across the
-- upgrade with no manual intervention.
--
-- legacy_daemon_id is a best-effort breadcrumb — we record whatever old
-- daemon_id value we rewrote last, which makes the consolidation auditable.
-- It also gives us a cheap index for locating rows that were migrated.
ALTER TABLE agent_runtime
    ADD COLUMN legacy_daemon_id TEXT;

CREATE INDEX idx_agent_runtime_legacy_daemon_id
    ON agent_runtime(workspace_id, provider, legacy_daemon_id)
    WHERE legacy_daemon_id IS NOT NULL;

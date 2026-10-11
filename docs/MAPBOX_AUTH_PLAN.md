# Mapbox CLI authentication (#11)

Tracked in [issue #11](https://github.com/adamcbrewer/shell-on-earth/issues/11).
Implementation continues on `feat/11-mapbox-cli-auth` from `3f24917`.

## Revised decisions

During implementation the user replaced the original bundling requirement with
an **optional external Mapbox CLI**. Builds/installations/releases ship only
`shoe`. The CLI is recommended for Mapbox features and required for browser login
and account statistics; an environment public token can render/search without it.
Select `mapbox` from PATH or `SHOE_MAPBOX_CLI`. v0.3.0 is live-tested; compatible
releases must expose the required `--schema`/JSON contract. No automatic downloads
or private bundled-executable fallback occur at runtime.

The user deleted `mapbox.example.json` and `mapbox.local.json`. Repo-local token
configuration has been removed. Precedence is now `MAPBOX_ACCESS_TOKEN`, then a
CLI-derived default public token. Explicit-empty environment remains a deliberate
opt-out of discovery/setup. Manual environment tokens remain supported for CI.

## Credential convention

Follow the [released Mapbox README](https://github.com/mapbox/mapbox-cli/tree/v0.3.0#auth),
[credential implementation](https://github.com/mapbox/mapbox-cli/blob/v0.3.0/src/auth.rs)
and [optional helper convention](https://github.com/mapbox/mapbox-cli/blob/v0.3.0/src/tilesets_cli.rs):

- The official installer defaults to `~/.local/bin/mapbox`; `MAPBOX_INSTALL_DIR`
  changes the executable destination.
- Mapbox CLI owns OAuth login, credential storage and refresh. The shared default
  profile uses `~/.mapbox/credentials.json`; `MAPBOX_CONFIG_DIR` relocates its store.
  Auth commands print that resolved path; JSON status includes `credential_file`.
- Released Unix credential files are 0600 and directories 0700, with atomic writes.
  The CLI stores plain JSON rather than using an OS keychain.
- `shoe` calls public CLI commands, never reads/writes those private files and
  never persists another public-token copy. Saved login works outside this repo.
- The CLI requests fixed general-purpose OAuth scopes, including write scopes
  `shoe` doesn't need; v0.3.0 has no scope-selection option. Retrieving public
  tokens needs `tokens:read`; reporting usage needs `statistics:read`. Packaging
  cannot narrow the grant. See [Mapbox scope guidance](https://docs.mapbox.com/accounts/guides/tokens/#scopes).
- Installation follows versioned/integrity-checked acquisition and reviewed
  updates from the [dependency reference](https://github.com/lirantal/npm-security-best-practices).

## Implementation

- `shoe auth login`, `logout`, `status [--json] [--usage]` share one adapter with
  rendering and viewer setup. Login opens the official browser flow and reports
  credential location separately from rendering readiness.
- JSON children use known fields, bounded output, cancellation and whole-process
  deadlines. Warning-prefixed stderr doesn't leak raw token-bearing responses.
  Account queries use `--use-login`, clear account/token/debug overrides and select
  the same default profile. HTTP-200 token verdicts are checked explicitly.
- Status reports login, rendering source/account, token validity and Streets
  access. Streets probes make tile requests. Statistics are optional and can't
  invalidate map access. Account/token reports share explicit UTC dates and carry
  timestamps; invalid/foreign identities don't receive token-specific statistics.
- Viewer discovery runs beside imports. File-free missing-credential launches
  offer setup/continuing offline; `--no-provider` and an empty token environment
  skip the offer. Palette
  setup hands the terminal to the CLI, then updates both map/search clients,
  cancels obsolete work and preserves workspace/camera/playback.
- CLI/MCP never launch browser login; MCP discovers credentials only for map
  renders. Offline imports, measurements, playback and manual journeys remain
  independent of account/CLI availability.
- Logout affects the selected shared profile, not token revocation, environment
  overrides or other processes' in-memory public tokens. Later launches must
  resolve the login again.
- Palette shortcuts lead the list; account setup/settings is last. Disconnected
  viewers offer setup, and connected viewers offer settings/checking rather than another sign-in.
  Rechecks report completion and success/failure. Offline planning remains
  enabled; Mapbox place search is dimmed/blocked until credentials are available.
- Installation guidance links to Mapbox's own documentation rather than copying
  installer scripts. Map credits are available inside Mapbox settings, independent
  of CLI login state; their attribution reflects contributing data.
- Mapbox CLI telemetry is off by default, with persistent opt-in under Mapbox
  settings. The preference uses the user's config directory and applies to
  `shoe`-launched CLI children; explicit environment opt-out takes precedence.

## Live verification

On 9 October 2026, the user completed existing-account browser login. The app's
status command subsequently retrieved the default public token, confirmed
`TokenValid`, rendered a Streets tile and obtained account/token Vector Tiles
reports with matching UTC periods and generation timestamps. The user then
deleted the repo token files; saved-login status still succeeded and reported
`/home/adam/.mapbox/credentials.json`. Token strings were captured internally.

New card-free signup/email verification, trial-token retrieval and trial-account
statistics access are **deferred by the user** for later external testing. Trial
support remains unconfirmed. If signup exceeds the CLI's five-minute browser
callback window, finish email verification and run login again. See
[account creation](https://docs.mapbox.com/accounts/guides/signup/) and
[current pricing](https://www.mapbox.com/pricing/).

Automated and terminal verification commands are in [validation](VALIDATION.md).

Verified for this implementation:

- `MAPBOX_ACCESS_TOKEN='' mise exec go@1.27.1 -- make format-check test race`:
  formatting, build, tests, vet, CLI/MCP parity and race checks pass.
- `python3 scripts/interactions.py`: complete offline interactions pass,
  including fake-CLI terminal handoff/restoration at 120×40, 70×18 and 30×12.
- `make mapbox-check` against v0.3.0: all six command schemas pass without
  account requests. Standalone staged installation works; the release archive
  contains only `shoe`.
- Live CLI rendering/status and MCP importing/rendering from `/tmp/opencode`
  use the saved login without repo token files. Both render Streets successfully.
  Early checks selected the verified external CLI with `SHOE_MAPBOX_CLI`. The
  user subsequently installed v0.3.0 on PATH and signed into the existing
  account through `shoe auth login`. Live status confirms usable login, valid
  rendering credentials, ready Streets access and CLI telemetry off.

# Durable decisions

This log keeps cross-cutting rationale that is not clearer in code, tests, the command contract, or the security contract.

Decision: Use Go with a conventional `cmd/` and `internal/` layout.
Context: Multicodex needs a portable local CLI with strong filesystem control and a small deployment surface.
Rationale: Go provides a static binary, mature standard library, straightforward concurrency, and familiar public-project structure.
Trade-offs: Go is more verbose than shell, and Windows remains unsupported.
References: `go.mod`, `cmd/multicodex/`, `internal/`

Decision: Produce release binaries only from version tags and inject one shared version.
Context: Public binaries need clear provenance, consistent CLI and provider-client identification, and checksums without hand-edited version constants.
Rationale: A tag-triggered workflow gives every target the same version and makes publication explicit.
Trade-offs: Untagged builds report a development version; release targets are limited to macOS and Linux on AMD64 and ARM64.
References: `internal/buildinfo/version.go`, `.github/workflows/release.yml`, `CONTRIBUTING.md`

Decision: Keep non-default account use entirely inside profile-local `CODEX_HOME` directories.
Context: Auth, sessions, threads, `/goal`, and other Codex state belong together, while the shared default Codex account remains outside multicodex ownership.
Rationale: Profile-local process environments let concurrent accounts operate without switching or sharing state. The default home can still serve as a protected final exec reserve and read-only monitor source without becoming managed state.
Trade-offs: Users must launch profile-scoped commands explicitly and sign in separately on each machine.
References: `docs/command-spec.md`, `docs/security-and-privacy.md`

Decision: Fail closed when profile filesystem ownership or auth isolation is ambiguous.
Context: Symlinks, hard links, permissive modes, path aliases, inherited account environment, and repository-local state can cross account or secret boundaries.
Rationale: Canonical path checks, private permissions, atomic writes, environment scrubbing, and narrow ownership rules keep failures visible and reduce the chance of changing or exposing another account's state.
Trade-offs: Some valid-looking custom layouts are rejected, and diagnostics intentionally omit raw external failure text.
References: `internal/codexstate/`, `internal/multicodex/config.go`, `internal/multicodex/security.go`, `internal/multicodex/doctor.go`, `docs/security-and-privacy.md`

Decision: Default persistent multicodex state to `~/multicodex`.
Context: Users may work from multiple repositories and worktrees.
Rationale: One stable home-level location avoids fragmented or accidentally committed profile state.
Trade-offs: Users who need another location must set `MULTICODEX_HOME`.
References: `internal/multicodex/paths.go`, `README.md`

Decision: Keep inspection commands non-mutating and provide one explicit reconciliation command.
Context: Help, completion, status, doctor, monitor, and dry-run are used as safe probes, while unattended resource maintenance still needs a supported path.
Rationale: Read-only discovery should never create state. `reconcile` reuses the established ownership and no-clobber rules without making inspection commands surprising.
Trade-offs: Resource changes require a separate explicit command or a profile-scoped launch path.
References: `internal/multicodex/app.go`, `internal/multicodex/reconcile.go`, `docs/command-spec.md`

Decision: Keep heartbeat profile-scoped, minimal, ephemeral, and cron-safe.
Context: A keepalive must verify logged-in profiles without persisting a session, touching a workspace, overlapping another run, or exposing subprocess output.
Rationale: A fixed read-only `hello`, bounded retry, and local non-blocking lock provide useful liveness with a narrow side-effect and reporting surface.
Trade-offs: Each logged-in profile sends a real request, and redacted failures provide less provider detail.
References: `internal/multicodex/heartbeat.go`, `docs/command-spec.md`

Decision: Integrate subscription monitoring under `multicodex monitor`.
Context: Account isolation and remaining subscription headroom are part of the same account-selection workflow.
Rationale: One product avoids a split workflow. Validated profile homes may use profile-scoped app-server reads with OAuth fallback; unvalidated homes stay on direct OAuth so their credential-store behavior is not guessed.
Trade-offs: Monitoring adds dependencies and may start read-only app-server processes for validated profiles. Transient full outages retain clearly marked stale official data rather than blanking useful context.
References: `internal/monitor/`, `internal/multicodex/monitor.go`, `docs/command-spec.md`

Decision: Normalize usage and routing around weekly default and Spark limits only.
Context: Weekly subscription limits are the useful account-selection signal, while older payloads may omit declared durations and expose the weekly window only in the secondary position.
Rationale: One weekly model keeps the monitor, metadata, observed estimates, and exec eligibility aligned. Declared 10,080-minute windows win; only the established secondary-position fallback remains for older payloads.
Trade-offs: Obsolete shorter limits are not exposed. Spark routing depends on a Spark model name and a Spark bucket for configured profiles.
References: `internal/monitor/usage/raw_types.go`, `internal/monitor/usage/select.go`, `internal/multicodex/exec.go`, `docs/command-spec.md`

Decision: Keep the default Codex home as the final `exec` reserve.
Context: A prompt should still have a destination when configured profiles are exhausted or unavailable.
Rationale: Configured profiles get normal weekly-aware selection, while the unmanaged default home is used only after they cannot accept the request.
Trade-offs: The final reserve can still fail in Codex when its usage data is unavailable or exhausted.
References: `internal/multicodex/exec.go`, `internal/monitor/usage/select.go`, `docs/command-spec.md`

Decision: Share normal Codex configuration defaults while preserving profile-local overrides.
Context: Model, reasoning, permission, and other Codex preferences should stay consistent across default and profile sessions without copied configuration.
Rationale: New profiles link to the default `config.toml`; regular profile config files remain explicit overrides. Every effective profile config must still select file-backed auth.
Trade-offs: A default config change affects linked profiles, and manual overrides must maintain their own compatible settings.
References: `internal/multicodex/config.go`, `internal/codexstate/config.go`, `README.md`

Decision: Keep profile guidance and skill sharing optional and no-clobber.
Context: Some users need common guidance or several skill sources, while existing profiles may contain intentional local files and runtime-managed `.system` skills.
Rationale: One optional `profile_resources` policy manages only documented symlink positions, preserves regular local overrides, and leaves omitted settings on their established behavior.
Trade-offs: Explicit management may retarget or remove symlinks at owned positions, so every such change is reported.
References: `internal/multicodex/resources.go`, `docs/command-spec.md`, `docs/security-and-privacy.md`

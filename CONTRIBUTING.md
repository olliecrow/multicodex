# Contributing

Thanks for helping improve multicodex.

## Development

Use the Go version declared in `go.mod`, then run:

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./...
```

Format Go changes with `gofmt`. If `pre-commit` is installed, enable the repository hooks with `pre-commit install --install-hooks`.

Keep changes narrow and preserve the contracts in `docs/command-spec.md` and `docs/security-and-privacy.md`. Update `README.md` and the command spec together when commands, flags, output, or routing behavior changes.

Tests and examples must use synthetic account data and portable paths. Never commit credentials, authentication state, private account details, or machine-specific paths. Report security issues through the process in `SECURITY.md`.

## Pull requests

Explain the user-visible outcome and the checks you ran. CI tests Linux and macOS, enables Go's race detector, runs `go vet`, builds the CLI, scans dependencies, and checks commits and patches for sensitive text.

## Releases

Maintainers create a version tag only after the main branch is green. Tags matching `v*` run the release workflow, which verifies the source and publishes checksummed macOS and Linux archives for AMD64 and ARM64. The tag is injected into CLI output and provider client identification.

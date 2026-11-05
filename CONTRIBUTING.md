# Contributing

Thanks for looking. This is a small, single-maintainer project; please open an
issue before a large change so you do not build something that will not be
merged.

## Before you start

Read [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md), especially **"Rules that
hold the design together"**. A change that breaks one of those six will be
declined even if it works.

## Setup

```sh
go build ./...
go vet ./...
go test ./...
```

Go 1.26+. No other toolchain, no code generation, no linter config beyond
`gofmt` and `go vet`. The only external dependency is the HTTP framework;
please do not add another without saying in the issue why it is worth it.

## Pull requests

- `gofmt` clean, `go vet` clean, `go test ./...` green.
- **Prove every test you add is real: break the code once, watch the test fail,
  then fix it.** Say so in the PR. A test nobody has seen fail is a guess.
- Never fix a red test by loosening its assertion. Change it deliberately and
  write the reason beside it.
- Comments should say *why*, not restate the code. Match the surrounding style.

## Things that will be declined

- **Anything that builds a shell command from a string.** Fixed argv or
  nothing. This is the single most important rule in the project.
- **Hard-coded machine, user, repository or path names.** They go in config.
- **An invented number.** No estimated quota shown as if measured, no
  percentage-complete that nothing computed. "Unknown" is a supported state;
  use it.
- **Widening the write surface** without a clear reason: this service can type
  into someone's agents.
- Provider logos or naming the project after someone else's trademark.

## Security

Do not open a public issue for a vulnerability — use a private GitHub security
advisory. See [docs/SECURITY.md](docs/SECURITY.md).

## Assets

Do not add images to `web/assets/` until the licensing question in
[docs/ASSETS.md](docs/ASSETS.md) is resolved, and never add one whose
provenance you cannot write down.

## Licence

Contributions are accepted under [Apache-2.0](LICENSE).

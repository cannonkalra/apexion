# Contributing to Apexion

Thanks for your interest in improving Apexion! Apexion is a single-binary data
lake exploration tool that crawls S3-compatible object storage, discovers
datasets, catalogs them into DuckDB, and lets you query them with SQL.
Contributions of all kinds are welcome, from bug reports to documentation fixes
to new features. This guide covers everything you need to get up and running.

## Prerequisites

- **Go 1.26+** (matches `go.mod`).
- **Docker** (optional) for running the full local stack.

The generated `*_templ.go` files and `assets/css/app.css` are committed to the
repository, so a normal build does **not** require the templ or Tailwind
toolchains. You only need those if you are modifying `.templ` templates or the
CSS (see [Development workflow](#development-workflow)).

## Getting started

```sh
git clone https://github.com/cannonkalra/apexion.git
cd apexion

make build   # compile the binary
make run     # run against an S3-compatible store on :9000
```

`make run` expects an S3-compatible object store reachable on `:9000`. If you
don't have one, `make up` starts a full stack in Docker (MinIO + a seed job +
the app) and serves the UI at http://localhost:8080.

## Development workflow

Common `make` targets:

| Target        | What it does                                    |
| ------------- | ----------------------------------------------- |
| `make fmt`    | Format the code                                 |
| `make vet`    | Run `go vet`                                     |
| `make lint`   | Run `golangci-lint`                             |
| `make test`   | Run the test suite                              |
| `make race`   | Run tests with the race detector               |
| `make check`  | Run fmt, vet, and tests together                |

Please run `make check` (and `make lint`, if you have
[golangci-lint](https://golangci-lint.run) installed) before opening a pull
request. CI runs all of these on every PR.

If you change `.templ` files or CSS, run `make assets` to regenerate the
committed `*_templ.go` files and `assets/css/app.css`, and commit those
regenerated files along with your change.

## Commit messages

This repo uses a bracketed category prefix followed by a short summary:

```
[Add] column insights
[Fix] multi select
[Polish] apply design system across all pages
[Test] catalog crawler edge cases
```

Common categories include `[Add]`, `[Fix]`, `[Polish]`, and `[Test]`. Keep the
summary short and imperative.

## Pull requests

- Keep PRs focused on a single change or concern.
- Ensure CI is green: build, vet, lint, and tests must pass across linux and
  macos.
- Update documentation and the CHANGELOG where relevant.

## Reporting bugs / requesting features

Please use [GitHub Issues](https://github.com/cannonkalra/apexion/issues) to report
bugs or request features. Include enough detail to reproduce the problem.

---

By participating, you agree to abide by our [Code of Conduct](CODE_OF_CONDUCT.md).
To report a security vulnerability, please follow our [Security Policy](SECURITY.md).

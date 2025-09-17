This was written with AI.

It works but code quality is not very good.

---

# gh-action-hasher

Pin GitHub Actions to immutable commit SHAs.

Given a directory, this tool finds all `.yml` files, locates lines like:

```
- uses: owner/repo[@path]/@ref
```

resolves `ref` (tag/branch) to its commit SHA via the GitHub API, and rewrites the file in place to:

```
- uses: owner/repo[@path]@<sha>
```

## Why

Actions referenced by tag/branch can drift. Pinning to a commit SHA makes builds reproducible and safer.

## Install

- With Go:
  - `go install github.com/goreleaser/gh-action-hasher@latest`
- From source:
  - `git clone` and `go build`

## Usage

- Process current directory:
  - `gh-action-hasher`
- Process a specific directory (recursively):
  - `gh-action-hasher .github/workflows`

Alternatively, run without installing:

- `go run .` (in this repo)
- `go run github.com/goreleaser/gh-action-hasher@latest <dir>`

## Environment

- `GITHUB_TOKEN` (recommended): increases rate limits and avoids anonymous API limits.

## What it changes

- Rewrites lines that look like one of these (quotes/comments preserved):
  - `uses: owner/repo@v1`
  - `- uses: owner/repo/path@v2  # comment`
  - `uses: "owner/repo@v1"`
  - `uses: 'owner/repo@v1'`

- Keeps as-is (no change) when value is:
  - local paths: `./…`, `../…`, `/…`
  - URLs or docker references: `https://…`, `docker://…`
  - expressions: contains `${{ … }}`
  - already a 40-char SHA

- Supports `owner/repo/path@ref` and reusable workflows like `owner/repo/.github/workflows/foo.yml@ref`.

## How it works (short)

- Regex-match single-line `uses:` entries
- Resolve `owner/repo@ref` to SHA via `GET /repos/{owner}/{repo}/commits/{ref}`
- Cache resolutions in-memory to avoid duplicate API calls
- Replace the line, preserving quotes/indentation/inline comments

## Limitations (intentional to keep it simple)

- Only `.yml` files are scanned (not `.yaml`)
- Only single-line `uses:` entries are handled
- Doesn’t parse YAML; complex scalars/anchors aren’t supported
- No flags (yet); runs quietly and prints only changed files

## Example

Before:

```
- uses: actions/checkout@v4  # keep
- uses: acme/foo/sub/act@v2
- uses: 'owner/with-single@v1'
- uses: "owner/with-quotes@v1"
```

After:

```
- uses: actions/checkout@<sha>  # keep
- uses: acme/foo/sub/act@<sha>
- uses: 'owner/with-single@<sha>'
- uses: "owner/with-quotes@<sha>"
```

## Testing

- `go test ./...`

Tests stub the GitHub API and operate on real workflow files in temp dirs. They verify formatting preservation (indentation, quotes, inline comments) and caching.

## Contributing

- See `PLAN.md` for the simplicity principles and current scope.
- PRs that keep the tool small and easy to read are welcome.

# Releasing Tokitoki CLI

## Branches and CI

Daily work goes to `dev`. A plain push to `dev` triggers no GitHub Actions
workflow. Open a pull request from `dev` to `main` when the changes are ready;
that pull request runs `go vet` and the race-enabled test suite. `main` is
protected and accepts changes only through pull requests. Merging the pull
request runs CI once more on `main`.

The repository has one maintainer, so a pull request is required but a second
person's approval is not. The required `test` check must pass, conversations
must be resolved, force pushes and deletion are disabled, and the same rules
apply to administrators.

## Cutting a release

Release tags must be semantic versions on an up-to-date `main` branch:

```sh
git switch main
git pull --ff-only
git tag v0.1.1
git push origin v0.1.1
```

The tag starts the `Release` workflow. It rejects tags outside `main`, reruns
vet and race-enabled tests, then cross-compiles stripped, reproducible binaries
for every supported target:

| Platform | amd64 | arm64 |
| --- | --- | --- |
| macOS (`darwin`) | `tokitoki-darwin-amd64` | `tokitoki-darwin-arm64` |
| Linux | `tokitoki-linux-amd64` | `tokitoki-linux-arm64` |
| Windows | `tokitoki-windows-amd64.exe` | `tokitoki-windows-arm64.exe` |

The workflow verifies the asset set and embedded version, generates
`checksums.txt`, and creates the GitHub Release. The executables intentionally
remain raw rather than being wrapped in ZIP or tar archives: the Tokitoki update
server proxies them directly to `tokitoki upgrade`.

Creating the GitHub Release does not publish it to clients. Import and publish
the version from `/admin/releases`; until then the update API ignores it.

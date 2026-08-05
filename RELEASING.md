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
git tag vX.Y.Z
git push origin vX.Y.Z
```

The tag pattern is exactly `v[0-9]+.[0-9]+.[0-9]+`. Suffixed tags — `v1.2.0-rc.1`,
`v1.2` — do not match the workflow trigger, so they build nothing and ship
nothing. There is no prerelease channel.

Never tag `dev`. The workflow's first step rejects a tag whose commit is not an
ancestor of `origin/main`, so tagging `dev` fails the build rather than shipping
untested work — but it leaves a junk tag to clean up.

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
server proxies them directly to `tokitoki update`.

The asset names are an API. The server matches the platform and arch tokens in
each filename to answer a download request, so renaming an asset or dropping one
from the matrix breaks clients on that platform. The workflow guards this: it
requires all six files to exist and the count to be exactly six.

Nothing in the repository records the version. `make cross VERSION=X.Y.Z` stamps
it through `-ldflags` into `internal/buildinfo.Version`, and the workflow derives
that value from the tag — so the tag is the only source of truth, and there is no
version file to bump in a commit. The workflow then runs the freshly built
binary and fails the release if `tokitoki version` disagrees with the tag.
Unstamped builds report `dev` and refuse to self-update, which is what keeps a
local build from overwriting itself with a release.

## Pushing the tag ships it

Creating the GitHub Release **is** publishing it. There is no second gate.

The update server answers `/api/updates/check` straight from the GitHub
Releases API (`lib/releases.ts` in `tracklm-nextjs`): no database mirror, no
publish switch, only a short-TTL in-memory cache that serves stale data when
GitHub is unreachable. The newest non-draft tag that parses as stable semver
becomes the answer for every client asking what to install. `/admin/releases`
reports downloads and is deliberately read-only — nothing on that page ships a
version.

So `git push origin vX.Y.Z` is the point of no return. Once the workflow
finishes, `tokitoki update` starts handing that binary to every client, and the
macOS/Windows apps and editor plugins follow, because they all delegate to the
same `tokitoki update`. Verify before pushing the tag, not after.

Backing out means acting before clients poll, and there is no way to recall
what has already been downloaded:

```sh
gh release delete vX.Y.Z --repo tokitoki-dev/tokitoki-cli --yes
git push origin :refs/tags/vX.Y.Z
git tag -d vX.Y.Z
```

Prefer rolling forward with a new patch version. Deleting a release that
clients have already seen means some of them sit on a version the server no
longer offers.

An earlier revision of this document described importing and publishing a
version from `/admin/releases`. That step no longer exists; the server was
changed to read GitHub directly.

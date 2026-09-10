# Contribution and branch protection

The default branch (`main`) is protected. **No pull request can be merged
without an approving review from the code owner (`@anunay999`).**

## How it is enforced

Two mechanisms, server-side first:

1. **`.github/CODEOWNERS`** marks every path as owned by `@anunay999`. GitHub
   automatically requests that review on every PR.
2. **Branch protection** on `main`:
   - require a pull request before merging
   - require **1** approving review, and **require code owner review**
   - dismiss stale approvals on new pushes
   - require approval of the most recent push
   - require all PR validation checks below to pass
   - require conversation resolution
   - block force pushes and branch deletion

### Required status checks

CI (`.github/workflows/ci.yml`) runs these on every pull request; all are required
before merge:

| Check | Validates |
|---|---|
| `fmt` | every Go file is `gofmt`-clean |
| `tidy` | `go.mod`/`go.sum` are tidy (no diff after `go mod tidy`) |
| `vet` | `go vet ./...` passes |
| `build` | compiles on the host and cross-compiles for linux/darwin × amd64/arm64 |
| `test` | `go test -race ./...` passes |
| `lint` | `staticcheck ./...` is clean |
| `vuln` | `govulncheck ./...` finds no known vulnerabilities |
| `shellcheck` | `install.sh` and the git hooks are warning-clean |
| `selfcheck` | the built CLI runs end to end (`config init/validate/get/set`, `schema`, `guide`, `setup`, `models`) |

`codeql` (`.github/workflows/codeql.yml`) also runs on pushes, pull requests, and
weekly. It is advisory rather than required, because `pull_request` runs from
forks cannot upload code-scanning results.

Because `enforce_admins` is left off, the repository owner can still push
directly in an emergency; everyone else must go through a reviewed PR. To make
the rule absolute (even the owner cannot bypass), set `enforce_admins: true` —
note that this then requires another collaborator to approve the owner's own PRs.

## Local hooks (defense in depth)

The repository ships opt-in hooks in `.githooks/`:

| Hook | Does |
|---|---|
| `pre-commit` | fails on unformatted Go files or a failing `go vet` |
| `commit-msg` | rejects empty or >100-char subjects |
| `pre-push` | runs `go test ./...`; blocks direct pushes to `main` unless `VECTOR_ALLOW_DIRECT_PUSH=1` |

Install them:

```sh
make hooks          # git config core.hooksPath .githooks
```

Local hooks are a convenience and can be bypassed with `--no-verify`; they do not
replace branch protection.

## Applying or updating branch protection

```sh
gh api --method PUT repos/anunay999/vector/branches/main/protection \
  --input - <<'JSON'
{
  "required_status_checks": {
    "strict": true,
    "contexts": ["fmt", "tidy", "vet", "build", "test", "lint", "vuln", "shellcheck", "selfcheck"]
  },
  "enforce_admins": false,
  "required_pull_request_reviews": {
    "dismiss_stale_reviews": true,
    "require_code_owner_reviews": true,
    "required_approving_review_count": 1,
    "require_last_push_approval": true
  },
  "required_conversation_resolution": true,
  "allow_force_pushes": false,
  "allow_deletions": false,
  "restrictions": null
}
JSON
```

Check it:

```sh
gh api repos/anunay999/vector/branches/main/protection
```

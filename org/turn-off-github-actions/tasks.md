# Tasks

## Fixes for turnofforgactions.go

- [x] **Fix resource leak** - Add `defer resp.Body.Close()` after successful HTTP request (line 84) and remove manual close logic (lines 96-99)
- [x] **Add nil check for repo.Name** - Guard against potential nil pointer dereference before using `*repo.Name`
- [x] **Add context timeout** - Add timeout to prevent indefinite hangs on large orgs
- [x] **Pass context to HTTP requests** - Use `http.NewRequestWithContext` so timeout applies to API calls
- [x] **Fix misleading comment** - Remove "uncomment if needed" from active code
- [x] **Use OAuth2 client for permissions API** - Switch from `http.DefaultClient` to `tc` for consistency

## Improvements (High Value)

- [x] **Skip archived repos** - Archived repos can't be modified, skip them to avoid guaranteed failures
- [x] **Add success/failure summary** - Track and display totals at the end instead of scrolling through logs
- [x] **Dry-run mode** - Add flag to preview what would be changed without making changes
- [x] **Use go-github for permissions** - N/A: go-github library doesn't wrap this specific endpoint; raw HTTP retained

## Improvements (Medium Value)

- [x] **Add command-line flags** - Support `--token`, `--org`, `--dry-run` flags alongside env vars
- [x] **Filter by repo name** - Add option to only process repos matching a pattern
- [ ] **Better rate limit handling** - Check `X-RateLimit` headers instead of fixed 1s delay

## Improvements (Lower Value)

- [ ] **Concurrent processing** - Process repos in parallel with worker pool (limited benefit due to rate limits)
- [x] **Update API version header** - Use `X-GitHub-Api-Version: 2022-11-28` instead of legacy `v3`
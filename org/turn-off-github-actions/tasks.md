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
- [x] **Better rate limit handling** - Check `X-RateLimit` headers instead of fixed 1s delay

## Improvements (Lower Value)

- [x] **Concurrent processing** - N/A: Minimal benefit (~5s savings), risk of triggering abuse detection
- [x] **Update API version header** - Use `X-GitHub-Api-Version: 2022-11-28` instead of legacy `v3`

## Bug Fixes (Found in Code Review)

- [x] **Fix rate limit exhaustion logic** - Line 250: condition `!success && err != nil` doesn't catch rate limit exhaustion case; should handle when `success=false` and `err=nil`
- [x] **Guard against nil resp dereference** - Line 255: `resp.StatusCode` could panic if `resp` is nil after failed retries
- [x] **Add nil checks to helper functions** - Lines 22, 60: `handleRateLimit` and `isRateLimited` should check for nil `resp`

## New Feature: JSONL Log File

- [x] **Add -log flag** - Add optional flag `-log=<filename>` that accepts an optional filename argument
- [x] **Generate default filename** - If `-log` provided without filename, generate `YYYY-MM-DD-HH-MM-SS-turnofforgactions.log`
- [x] **Create LogEntry struct** - Define struct for JSONL log entries with fields: timestamp, repo, action, status, message, etc.
- [x] **Create JSON logger** - Helper function to write LogEntry structs as JSON lines to file
- [x] **Open/close log file** - Open file at start (if flag set), defer close, handle errors
- [x] **Log each repo action** - Write JSONL entry for: skipped, dry-run, success, failed, rate-limited
- [x] **Log summary** - Write final summary entry with totals
- [x] **Dual output** - Continue logging to stdout while also writing to file
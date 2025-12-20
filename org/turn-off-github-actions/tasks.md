# Tasks

## Fixes for turnofforgactions.go

- [x] **Fix resource leak** - Add `defer resp.Body.Close()` after successful HTTP request (line 84) and remove manual close logic (lines 96-99)
- [x] **Add nil check for repo.Name** - Guard against potential nil pointer dereference before using `*repo.Name`
- [x] **Add context timeout** - Add timeout to prevent indefinite hangs on large orgs
- [x] **Pass context to HTTP requests** - Use `http.NewRequestWithContext` so timeout applies to API calls
- [x] **Fix misleading comment** - Remove "uncomment if needed" from active code
- [x] **Use OAuth2 client for permissions API** - Switch from `http.DefaultClient` to `tc` for consistency
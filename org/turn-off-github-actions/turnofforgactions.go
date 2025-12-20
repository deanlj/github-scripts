package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"strconv"
	"time"

	"github.com/google/go-github/v72/github"
	"golang.org/x/oauth2"
)

// handleRateLimit checks response headers and waits if rate limit is low
func handleRateLimit(resp *http.Response) {
	remaining := resp.Header.Get("X-RateLimit-Remaining")
	resetHeader := resp.Header.Get("X-RateLimit-Reset")

	if remaining == "" {
		return
	}

	remainingInt, err := strconv.Atoi(remaining)
	if err != nil {
		return
	}

	// If we have plenty of quota, no delay needed
	if remainingInt > 100 {
		return
	}

	// If quota is getting low, add a small delay
	if remainingInt > 10 {
		time.Sleep(100 * time.Millisecond)
		return
	}

	// If quota is very low, wait until reset
	if resetHeader != "" {
		resetTime, err := strconv.ParseInt(resetHeader, 10, 64)
		if err == nil {
			waitDuration := time.Until(time.Unix(resetTime, 0))
			if waitDuration > 0 {
				log.Printf("Rate limit low (%d remaining), waiting %v until reset", remainingInt, waitDuration.Round(time.Second))
				time.Sleep(waitDuration + time.Second)
			}
		}
	}
}

// isRateLimited checks if the response indicates a rate limit error
func isRateLimited(resp *http.Response) bool {
	if resp.StatusCode == 429 {
		return true
	}
	if resp.StatusCode == 403 {
		remaining := resp.Header.Get("X-RateLimit-Remaining")
		if remaining == "0" {
			return true
		}
	}
	return false
}

func main() {
	// Parse command-line flags
	tokenFlag := flag.String("token", "", "GitHub personal access token (or set GITHUB_TOKEN env var)")
	orgFlag := flag.String("org", "", "GitHub organization name (or set GITHUB_ORG env var)")
	filterFlag := flag.String("filter", "", "Only process repos matching pattern (supports * and ? wildcards)")
	dryRun := flag.Bool("dry-run", false, "Preview changes without making them")
	flag.Parse()

	// Get credentials from flags or environment variables
	token := *tokenFlag
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	org := *orgFlag
	if org == "" {
		org = os.Getenv("GITHUB_ORG")
	}

	// Validate inputs
	if token == "" || org == "" {
		fmt.Println("Please provide both token and org via flags or environment variables.")
		fmt.Println()
		fmt.Println("Usage:")
		fmt.Println("  turnofforgactions -token=<token> -org=<org> [--dry-run]")
		fmt.Println()
		fmt.Println("Or set environment variables:")
		fmt.Println("  export GITHUB_TOKEN=your_personal_access_token")
		fmt.Println("  export GITHUB_ORG=your_organization_name")
		os.Exit(1)
	}

	// Create GitHub client with authentication for listing repos
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	// Get list of repositories for the organization
	opt := &github.RepositoryListByOrgOptions{
		ListOptions: github.ListOptions{PerPage: 100},
	}

	var allRepos []*github.Repository
	for {
		repos, resp, err := client.Repositories.ListByOrg(ctx, org, opt)
		if err != nil {
			log.Fatalf("Error fetching repositories: %v", err)
		}
		allRepos = append(allRepos, repos...)
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}

	log.Printf("Found %d repositories in organization %s\n", len(allRepos), org)

	if *dryRun {
		log.Println("DRY-RUN MODE: No changes will be made")
	}

	// Counters for summary
	var successCount, failedCount, skippedCount int

	// Loop through repositories and disable GitHub Actions using direct API calls
	for _, repo := range allRepos {
		if repo.Name == nil {
			log.Printf("Skipping repository with nil name")
			skippedCount++
			continue
		}
		if repo.Archived != nil && *repo.Archived {
			log.Printf("Skipping archived repository: %s", *repo.Name)
			skippedCount++
			continue
		}
		if *filterFlag != "" {
			matched, err := path.Match(*filterFlag, *repo.Name)
			if err != nil {
				log.Printf("Invalid filter pattern: %v", err)
				os.Exit(1)
			}
			if !matched {
				skippedCount++
				continue
			}
		}
		if *dryRun {
			log.Printf("[DRY-RUN] Would disable GitHub Actions for: %s", *repo.Name)
			successCount++
			continue
		}

		log.Printf("Updating repository: %s", *repo.Name)

		// Create request to disable GitHub Actions
		url := fmt.Sprintf("https://api.github.com/repos/%s/%s/actions/permissions", org, *repo.Name)
		payload := map[string]bool{"enabled": false}
		jsonPayload, err := json.Marshal(payload)
		if err != nil {
			log.Printf("Error creating JSON payload for %s: %v", *repo.Name, err)
			failedCount++
			continue
		}

		req, err := http.NewRequestWithContext(ctx, "PUT", url, bytes.NewBuffer(jsonPayload))
		if err != nil {
			log.Printf("Error creating request for %s: %v", *repo.Name, err)
			failedCount++
			continue
		}

		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

		// Send request with retry logic for rate limits
		var resp *http.Response
		var body []byte
		maxRetries := 3
		success := false

		for attempt := 0; attempt < maxRetries; attempt++ {
			if attempt > 0 {
				// Recreate request for retry (body was consumed)
				req, err = http.NewRequestWithContext(ctx, "PUT", url, bytes.NewBuffer(jsonPayload))
				if err != nil {
					log.Printf("Error creating retry request for %s: %v", *repo.Name, err)
					break
				}
				req.Header.Set("Accept", "application/vnd.github+json")
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
			}

			resp, err = tc.Do(req)
			if err != nil {
				log.Printf("Error sending request for %s: %v", *repo.Name, err)
				break
			}

			body, err = io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				log.Printf("Error reading response for %s: %v", *repo.Name, err)
				break
			}

			// Check for rate limiting
			if isRateLimited(resp) {
				resetHeader := resp.Header.Get("X-RateLimit-Reset")
				if resetHeader != "" {
					resetTime, parseErr := strconv.ParseInt(resetHeader, 10, 64)
					if parseErr == nil {
						waitDuration := time.Until(time.Unix(resetTime, 0))
						if waitDuration > 0 {
							log.Printf("Rate limited, waiting %v until reset (attempt %d/%d)", waitDuration.Round(time.Second), attempt+1, maxRetries)
							time.Sleep(waitDuration + time.Second)
							continue
						}
					}
				}
				// Exponential backoff if no reset header
				backoff := time.Duration(1<<attempt) * time.Second
				log.Printf("Rate limited, backing off %v (attempt %d/%d)", backoff, attempt+1, maxRetries)
				time.Sleep(backoff)
				continue
			}

			// Success or non-rate-limit error
			success = true
			break
		}

		if !success && err != nil {
			failedCount++
			continue
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			log.Printf("Successfully disabled GitHub Actions for repository: %s", *repo.Name)
			successCount++
		} else {
			log.Printf("Failed to disable GitHub Actions for %s. Status: %d, Response: %s",
				*repo.Name, resp.StatusCode, string(body))
			failedCount++
		}

		// Adaptive rate limit handling
		handleRateLimit(resp)
	}

	log.Println("Finished updating all repositories.")
	log.Printf("Summary: %d succeeded, %d failed, %d skipped", successCount, failedCount, skippedCount)
}

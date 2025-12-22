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
	"net/url"
	"os"
	"path"
	"strconv"
	"time"

	"github.com/google/go-github/v72/github"
	"golang.org/x/oauth2"
)

// LogEntry represents a single log entry in JSONL format
type LogEntry struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Repo      string `json:"repo,omitempty"`
	Action    string `json:"action,omitempty"`
	Status    string `json:"status,omitempty"`
	Message   string `json:"message,omitempty"`
	Succeeded int    `json:"succeeded,omitempty"`
	Failed    int    `json:"failed,omitempty"`
	Skipped   int    `json:"skipped,omitempty"`
}

// JSONLogger handles writing log entries to a JSONL file
type JSONLogger struct {
	file    *os.File
	encoder *json.Encoder
}

// NewJSONLogger creates a new JSON logger for the given file path
func NewJSONLogger(filePath string) (*JSONLogger, error) {
	file, err := os.Create(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create log file: %w", err)
	}
	return &JSONLogger{
		file:    file,
		encoder: json.NewEncoder(file),
	}, nil
}

// Log writes a log entry to the JSONL file
func (l *JSONLogger) Log(entry LogEntry) {
	if l == nil || l.encoder == nil {
		return
	}
	entry.Timestamp = time.Now().UTC().Format(time.RFC3339)
	_ = l.encoder.Encode(entry)
}

// Close closes the log file
func (l *JSONLogger) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	return l.file.Close()
}

// handleRateLimit checks response headers and waits if rate limit is low
func handleRateLimit(resp *http.Response) {
	if resp == nil {
		return
	}
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
	if resp == nil {
		return false
	}
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
	logFlag := flag.String("log", "", "Write JSONL log to file (optional filename, defaults to timestamp-based name)")
	flag.Parse()

	// Setup JSON logger if -log flag is provided
	var jsonLogger *JSONLogger
	// Check if -log was explicitly provided (even without value)
	logFlagProvided := false
	for _, arg := range os.Args[1:] {
		if arg == "-log" || arg == "--log" || len(arg) > 4 && arg[:5] == "-log=" {
			logFlagProvided = true
			break
		}
	}
	if logFlagProvided {
		logFile := *logFlag
		if logFile == "" {
			// Generate default filename with timestamp
			logFile = time.Now().Format("2006-01-02-15-04-05") + "-turnofforgactions.log"
		}
		var err error
		jsonLogger, err = NewJSONLogger(logFile)
		if err != nil {
			log.Fatalf("Failed to create log file: %v", err)
		}
		defer jsonLogger.Close()
		log.Printf("Writing JSONL log to: %s", logFile)
	}

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
		fmt.Println("  turnofforgactions -token=<token> -org=<org> [-dry-run]")
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
			// Check if it's a rate limit error
			if rateLimitErr, ok := err.(*github.RateLimitError); ok {
				waitDuration := time.Until(rateLimitErr.Rate.Reset.Time)
				log.Printf("Rate limited while fetching repositories, waiting %v until reset...", waitDuration.Round(time.Second))
				time.Sleep(waitDuration + time.Second)
				continue
			}
			log.Fatalf("Error fetching repositories: %v", err)
		}
		allRepos = append(allRepos, repos...)

		// Check rate limit and wait if getting low
		if resp.Rate.Remaining < 10 {
			waitDuration := time.Until(resp.Rate.Reset.Time)
			if waitDuration > 0 {
				log.Printf("Rate limit low (%d remaining), waiting %v until reset", resp.Rate.Remaining, waitDuration.Round(time.Second))
				time.Sleep(waitDuration + time.Second)
			}
		}

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
			jsonLogger.Log(LogEntry{Type: "repo", Action: "skip", Status: "skipped", Message: "Repository has nil name"})
			skippedCount++
			continue
		}
		if repo.Archived != nil && *repo.Archived {
			log.Printf("Skipping archived repository: %s", *repo.Name)
			jsonLogger.Log(LogEntry{Type: "repo", Repo: *repo.Name, Action: "skip", Status: "skipped", Message: "Repository is archived"})
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
				jsonLogger.Log(LogEntry{Type: "repo", Repo: *repo.Name, Action: "skip", Status: "skipped", Message: "Does not match filter pattern"})
				skippedCount++
				continue
			}
		}
		if *dryRun {
			log.Printf("[DRY-RUN] Would disable GitHub Actions for: %s", *repo.Name)
			jsonLogger.Log(LogEntry{Type: "repo", Repo: *repo.Name, Action: "disable_actions", Status: "dry_run", Message: "Would disable GitHub Actions"})
			successCount++
			continue
		}

		log.Printf("Updating repository: %s", *repo.Name)

		// Create request to disable GitHub Actions
		apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/actions/permissions", url.PathEscape(org), url.PathEscape(*repo.Name))
		payload := map[string]bool{"enabled": false}
		jsonPayload, err := json.Marshal(payload)
		if err != nil {
			log.Printf("Error creating JSON payload for %s: %v", *repo.Name, err)
			failedCount++
			continue
		}

		req, err := http.NewRequestWithContext(ctx, "PUT", apiURL, bytes.NewBuffer(jsonPayload))
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
				req, err = http.NewRequestWithContext(ctx, "PUT", apiURL, bytes.NewBuffer(jsonPayload))
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
			_ = resp.Body.Close()
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

		if !success {
			if err != nil {
				// Request or read error
				jsonLogger.Log(LogEntry{Type: "repo", Repo: *repo.Name, Action: "disable_actions", Status: "failed", Message: err.Error()})
				failedCount++
			} else {
				// Rate limit exhaustion (all retries failed due to rate limiting)
				log.Printf("Rate limit exhausted for %s after %d attempts", *repo.Name, maxRetries)
				jsonLogger.Log(LogEntry{Type: "repo", Repo: *repo.Name, Action: "disable_actions", Status: "failed", Message: fmt.Sprintf("Rate limit exhausted after %d attempts", maxRetries)})
				failedCount++
			}
			continue
		}

		if resp != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			log.Printf("Successfully disabled GitHub Actions for repository: %s", *repo.Name)
			jsonLogger.Log(LogEntry{Type: "repo", Repo: *repo.Name, Action: "disable_actions", Status: "success", Message: "Successfully disabled GitHub Actions"})
			successCount++
		} else if resp != nil {
			log.Printf("Failed to disable GitHub Actions for %s. Status: %d, Response: %s",
				*repo.Name, resp.StatusCode, string(body))
			jsonLogger.Log(LogEntry{Type: "repo", Repo: *repo.Name, Action: "disable_actions", Status: "failed", Message: fmt.Sprintf("Status %d: %s", resp.StatusCode, string(body))})
			failedCount++
		} else {
			log.Printf("Failed to disable GitHub Actions for %s: no response", *repo.Name)
			jsonLogger.Log(LogEntry{Type: "repo", Repo: *repo.Name, Action: "disable_actions", Status: "failed", Message: "No response received"})
			failedCount++
		}

		// Adaptive rate limit handling
		handleRateLimit(resp)
	}

	log.Println("Finished updating all repositories.")
	log.Printf("Summary: %d succeeded, %d failed, %d skipped", successCount, failedCount, skippedCount)

	// Write summary to JSONL log
	jsonLogger.Log(LogEntry{
		Type:      "summary",
		Succeeded: successCount,
		Failed:    failedCount,
		Skipped:   skippedCount,
		Message:   "Finished updating all repositories",
	})
}

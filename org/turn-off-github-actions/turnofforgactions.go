package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/google/go-github/v72/github"
	"golang.org/x/oauth2"
)

func main() {
	// Get credentials from environment variables
	token := os.Getenv("GITHUB_TOKEN")
	org := os.Getenv("GITHUB_ORG")

	// Validate inputs
	if token == "" || org == "" {
		fmt.Println("Please set both GITHUB_TOKEN and GITHUB_ORG environment variables.")
		fmt.Println("Example:")
		fmt.Println("  export GITHUB_TOKEN=your_personal_access_token")
		fmt.Println("  export GITHUB_ORG=your_organization_name")
		os.Exit(1)
	}

	// Create GitHub client with authentication for listing repos
	ctx := context.Background()
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

	// Loop through repositories and disable GitHub Actions using direct API calls
	for _, repo := range allRepos {
		log.Printf("Updating repository: %s", *repo.Name)

		// Create request to disable GitHub Actions
		url := fmt.Sprintf("https://api.github.com/repos/%s/%s/actions/permissions", org, *repo.Name)
		payload := map[string]bool{"enabled": false}
		jsonPayload, err := json.Marshal(payload)
		if err != nil {
			log.Printf("Error creating JSON payload for %s: %v", *repo.Name, err)
			continue
		}

		req, err := http.NewRequest("PUT", url, bytes.NewBuffer(jsonPayload))
		if err != nil {
			log.Printf("Error creating request for %s: %v", *repo.Name, err)
			continue
		}

		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github.v3+json")
		req.Header.Set("Content-Type", "application/json")

		// Send request
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Printf("Error sending request for %s: %v", *repo.Name, err)
			continue
		}

		// Read and handle response
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Printf("Error reading response for %s: %v", *repo.Name, err)
			continue
		}
		errBody := resp.Body.Close()
		if errBody != nil {
			continue
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			log.Printf("Successfully disabled GitHub Actions for repository: %s", *repo.Name)
		} else {
			log.Printf("Failed to disable GitHub Actions for %s. Status: %d, Response: %s",
				*repo.Name, resp.StatusCode, string(body))
		}

		// Optional: Add a delay to avoid hitting API rate limits
		time.Sleep(1 * time.Second) // Uncomment if needed
	}

	log.Println("Finished updating all repositories.")
}

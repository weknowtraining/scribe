package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/darkhelmet/env"
	"github.com/google/go-github/github"
	"golang.org/x/oauth2"
)

var (
	start, end, owner, repo, token, jira, regex string
	pullRequestRe                               = regexp.MustCompile(`Merge pull request #(\d+)`)
	ticketRe                                    *regexp.Regexp
	dryRun                                      bool
)

func ensure(values ...string) {
	for _, value := range values {
		if value == "" {
			log.Println("missing flags")
			flag.PrintDefaults()
			os.Exit(1)
		}
	}
}

func init() {
	flag.StringVar(&start, "start", env.StringDefault("SCRIBE_START", ""), "Where to start the compare")
	flag.StringVar(&end, "end", env.StringDefault("SCRIBE_END", ""), "Where to end the compare")
	flag.StringVar(&owner, "owner", env.StringDefault("SCRIBE_OWNER", ""), "The repository owner")
	flag.StringVar(&repo, "repo", env.StringDefault("SCRIBE_REPO", ""), "The repository name")
	flag.StringVar(&token, "token", env.StringDefault("SCRIBE_TOKEN", ""), "Access token")
	flag.StringVar(&jira, "jira", env.StringDefault("SCRIBE_JIRA", ""), "The JIRA host to link issues to")
	flag.StringVar(&regex, "regex", env.StringDefault("SCRIBE_REGEX", ""), "The JIRA project key regex to use (like ABC|BSD")
	flag.BoolVar(&dryRun, "dryrun", false, "Do a dry run (do not create a release, just print it")
	flag.Parse()
	ensure(start, end, owner, repo, token, jira, regex)
	re, err := regexp.Compile(fmt.Sprintf(`(?:%s)-(?:\d+)`, regex))
	if err != nil {
		log.Fatalf("regex was invalid: %s", err)
	}
	ticketRe = re
}

func parsePullRequestNumber(message string) string {
	matches := pullRequestRe.FindStringSubmatch(message)
	if len(matches) > 0 {
		return matches[1]
	}
	return ""
}

func makeReleaseName() string {
	return time.Now().Format("2006-01-02.1504")
}

func main() {
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	// Get the compare view
	compare, _, err := client.Repositories.CompareCommits(ctx, owner, repo, start, end)
	if err != nil {
		log.Fatalf("failed comparing commits: %s", err)
	}

	// Extract the PR numbers
	pullRequestNumbers := make(chan int)
	go func() {
		for _, commit := range compare.Commits {
			id := parsePullRequestNumber(*commit.Commit.Message)
			if id != "" {
				num, err := strconv.Atoi(id)
				if err != nil {
					log.Fatalf("failed parsing pull request number: %s", err)
				}
				pullRequestNumbers <- num
			}
		}
		close(pullRequestNumbers)
	}()

	// Extract the titles of the PRs, format into lines
	lines := make(chan string)
	go func() {
		for id := range pullRequestNumbers {
			pr, _, err := client.PullRequests.Get(ctx, owner, repo, id)
			if err != nil {
				log.Fatalf("failed getting pull request: %s", err)
			}
			title := ticketRe.ReplaceAllStringFunc(*pr.Title, func(ticket string) string {
				return fmt.Sprintf("[%s](https://%s/browse/%s)", ticket, jira, ticket)
			})
			lines <- fmt.Sprintf("- %s #%d", title, id)
		}
		close(lines)
	}()
	bodyLines := make([]string, 0)
	for line := range lines {
		bodyLines = append(bodyLines, line)
	}
	body := strings.Join(bodyLines, "\n")
	name := makeReleaseName()
	if dryRun {
		log.Printf("dry run\n%s\n\n%s", name, body)
	} else {
		release, _, err := client.Repositories.CreateRelease(ctx, owner, repo, &github.RepositoryRelease{
			TagName:         &name,
			TargetCommitish: &end,
			Name:            &name,
			Body:            &body,
		})
		if err != nil {
			log.Fatalf("failed creating release: %s", err)
		}
		log.Printf("created release %s", *release.Name)
	}
}

package main

import (
	"context"
	"time"

	"github.com/google/go-github/v36/github"
	log "github.com/sirupsen/logrus"
	"golang.org/x/oauth2"
)

var (
	// GithubClient is a Github API client, authenticated using the k8s API Key secret
	GithubClient *github.Client
)

// Runs at startup - initializes Github API client
func InitGithubClient(githubAuthToken string) {
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: githubAuthToken},
	)
	tc := oauth2.NewClient(ctx, ts)
	GithubClient = github.NewClient(tc)
}

// runs on a ticker - gets the latest Github checks for each repo
func GetLatestGithubBuilds() {
	// before getting from Github, see if we need to update today's date.
	now := time.Now()
	if DateAfter(latestTimestamp.Time, now) {
		TurnoverDate()
	}

	for i, r := range Repos {
		// call Github API to get latest checks for repo
		result, _, err := GithubClient.Checks.ListCheckRunsForRef(context.Background(), r.Owner, r.Name, r.DefaultBranch, &github.ListCheckRunsOptions{})
		if err != nil {
			log.Error(err)
			continue
		}

		checkRuns := result.CheckRuns
		if len(checkRuns) == 0 {
			log.Debugf("Did not find any checks for repo: %s", r.Link)
			continue
		}

		// create a new Build from this latest batch of CheckRuns. 1 build = 1 commit for that repo.
		newBuild := Build{
			Timestamp: checkRuns[0].StartedAt,
			Datefmt:   GenerateDatefmt(*checkRuns[0].StartedAt),
			Status:    GenerateCompositeStatus(checkRuns),
			Url:       *checkRuns[0].HTMLURL,
			Sha:       GenerateShortSha(*checkRuns[0].HeadSHA),
		}
		// update Repo builds with the appropriate day
		// the reason we can't just update "today" is that sometimes, the latest build wasn't today, it was a few days ago, etc.
		for j, b := range r.Builds {
			if b.Datefmt == newBuild.Datefmt {
				Repos[i].Builds[j] = newBuild
			}
		}
	}
	SetUnknownsToPrevDay() // "bleed" dates into subsequent days, if unknowns. ie. allow a failing build 2 days ago to show up as still failing today, if no builds have run since then.
	log.Debug("🔼 Updated Github builds.")
}

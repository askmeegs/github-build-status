package main

import (
	"context"
	"fmt"
	"os"
	"time"

	rice "github.com/GeertJohan/go.rice"
	"github.com/foolin/gin-template/supports/gorice"
	"github.com/gin-gonic/gin"
	"github.com/google/go-github/v36/github"
	"github.com/jinzhu/configor"
	log "github.com/sirupsen/logrus"
	"golang.org/x/oauth2"

	"net/http"

	"github.com/lensesio/tableprinter"
)

const (
	SUCCESS = "success" // green
	FAILURE = "failure" // red
	PENDING = "pending" // yellow
	UNKNOWN = "unknown" // gray
)

// A Build represents the latest build result for a specific day
type Build struct {
	Timestamp *github.Timestamp `header:"timestamp"`
	Status    string            `header:"status"`
	Url       string            `header:"URL"`
}

// A Repo stores metadata about the repo, and builds from the last <dayHistory> Builds
type Repo struct {
	Link          string  `header:"link"`
	Owner         string  `header:"owner"`
	Name          string  `header:"name"`
	DefaultBranch string  `header:"default branch"`
	Builds        []Build `header:"Builds"`
}

var (
	dayHistory  int
	githubToken string
	client      *github.Client
	Repos       []Repo
	Dates       []string //formatted dates to use for the grid
)

// User config
var Config = struct {
	DayHistory  int    `default:"7"`
	GithubToken string `required:"true"`

	Repos []struct {
		Owner         string `required:"true"`
		Name          string `required:"true"`
		DefaultBranch string `default:"main"`
	}
}{}

func main() {
	log.Info("😺 Starting up..")
	loadConfig()

	// set up Github client
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: githubToken},
	)
	tc := oauth2.NewClient(ctx, ts)
	client = github.NewClient(tc)

	ResetDays() //initial setup
	UpdateAllRepos()
	PrintGrid()

	// Start ticker to update Repos' Build data
	ticker := time.NewTicker(3 * time.Second)
	quit := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				UpdateAllRepos()
			case <-quit:
				ticker.Stop()
				return
			}
		}
	}()

	// Define handlers
	r := gin.Default()
	staticBox := rice.MustFindBox("static")
	r.StaticFS("/static", staticBox.HTTPBox())
	r.HTMLRender = gorice.New(rice.MustFindBox("views"))

	r.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"message": "pong",
		})
	})

	// Index.html handler - display build status as a grid.
	r.GET("/", func(c *gin.Context) {
		c.HTML(http.StatusOK, "templates/index.html", gin.H{"dates": Dates, "repos": Repos})
	})

	r.Run() // localhost:8080
}

// Runs before startup. Get config from file. (configmap)
func loadConfig() error {
	configor.Load(&Config, "config.yml")
	dayHistory = Config.DayHistory
	githubToken = Config.GithubToken

	Repos = []Repo{}
	for _, r := range Config.Repos {
		repoStruct := Repo{
			Owner:         r.Owner,
			Name:          r.Name,
			Link:          fmt.Sprintf("https://github.com/%s/%s", r.Owner, r.Name),
			DefaultBranch: r.DefaultBranch,
			Builds:        []Build{},
		}
		Repos = append(Repos, repoStruct)
	}
	return nil
}

// Runs on a ticker. For each Repo, get the latest Build status and update the grid.
func UpdateAllRepos() error {
	for _, r := range Repos {
		log.Infof("\n\n🔄 Updating repo: %s/%s", r.Owner, r.Name)
		err := updateRepo(&r)
		if err != nil {
			log.Error(err)
			continue
		}
	}
	SetUnknownsToPrevDay()
	log.Info("🚀 Update complete. Repos listed below.\n\n")
	PrintGrid()
	return nil
}

// process dates + get the latest Build status
func updateRepo(r *Repo) error {
	// Get check runs for repo
	log.Info("Getting check runs for repo: %s/%s", r.Owner, r.Name)
	result, _, err := client.Checks.ListCheckRunsForRef(context.Background(), r.Owner, r.Name, r.DefaultBranch, &github.ListCheckRunsOptions{})
	if err != nil {
		return err
	}
	checkRuns := result.CheckRuns

	latestRunTimestamp := &github.Timestamp{time.Now()}
	url := ""

	// Exact Timestamp and URL matter less. just get the first check run's info for that commit.
	if len(checkRuns) > 0 {
		firstCheckRun := checkRuns[0]
		latestRunTimestamp = firstCheckRun.StartedAt
		url = *firstCheckRun.HTMLURL
	}

	// Status matters more. set the composite status to a fallthrough value - where it's success only if all the runs are successful, failure if a single run fails, and pending if any run is still running.
	compositeStatus := SUCCESS
	if len(checkRuns) == 0 {
		compositeStatus = UNKNOWN
	}
	for _, checkRun := range checkRuns {
		if *checkRun.Status == "queued" || *checkRun.Status == "in_progress" {
			compositeStatus = PENDING
		} else if *checkRun.Conclusion == FAILURE {
			compositeStatus = FAILURE
		}
	}

	// Create a new Build representing the latest build for this repo
	newBuild := Build{
		Timestamp: latestRunTimestamp,
		Status:    compositeStatus,
		Url:       url,
	}

	log.Infof("⭐️ New Build: %v for Repo: %s", newBuild, r.Name)

	// Update the repo's Builds slice with the latest build
	updateDay(r, newBuild)
	return nil
}

func updateDay(repo *Repo, newBuild Build) {
	// Find the index of the build to be overwritten.
	for i, build := range repo.Builds {
		if DateEqual(build.Timestamp.Time, newBuild.Timestamp.Time) {
			log.Infof("📆 Updating day: %v, with new build info", build.Timestamp.Time)
			repo.Builds[i] = newBuild
			return
		}
	}
	log.Warn("Got a new build but did not insert")
}

// Helper function. Runs at startup, and on the ticker
// but ONLY IF the latest build is not the same day as today.
func ResetDays() {
	log.Info("🗓️ Resetting days...")
	for i, r := range Repos {
		oldDayMap := CreateDayMap(r.Builds)
		newDays := CreateResetDays()
		for _, newDay := range newDays {
			if val, ok := oldDayMap[newDay.Timestamp.Time]; ok {
				newDay.Status = val.Status
				newDay.Url = val.Url
			}
		}
		r.Builds = newDays
		Repos[i] = r
	}
	SetUnknownsToPrevDay()
}

func CreateResetDays() []Build {
	now := time.Now()
	newDays := []Build{}
	Dates = []string{}
	for i := 0; i < dayHistory; i++ {
		then := now.AddDate(0, 0, -i)
		_, m, d := then.Date()
		dateFmt := fmt.Sprintf("%d/%d", m, d)
		Dates = append([]string{dateFmt}, Dates...)
		b := Build{
			Timestamp: &github.Timestamp{then},
			Status:    UNKNOWN,
			Url:       "",
		}
		newDays = append([]Build{b}, newDays...) //prepend historical date to front
	}
	return newDays
}

func CreateDayMap(Builds []Build) map[time.Time]Build {
	// get the date only as key
	dayMap := make(map[time.Time]Build)
	for _, b := range Builds {
		y, m, d := b.Timestamp.Time.Date()
		newDate := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
		dayMap[newDate] = Build{Timestamp: &github.Timestamp{newDate}, Status: b.Status, Url: b.Url}
	}
	return dayMap
}

// Helper function - returns true if two timestamps have the same calendar date.
func DateEqual(date1, date2 time.Time) bool {
	y1, m1, d1 := date1.Date()
	y2, m2, d2 := date2.Date()
	return y1 == y2 && m1 == m2 && d1 == d2
}

// Helper function - returns true if two timestamps have the same calendar date.
// zeroes out the time because we only care about dates.
func DateAfter(date1, date2 time.Time) bool {
	y1, m1, d1 := date1.Date()
	y2, m2, d2 := date2.Date()

	t1 := time.Date(y1, m1, d1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(y2, m2, d2, 0, 0, 0, 0, time.UTC)

	return t2.After(t1)
}

func PrintGrid() {
	printer := tableprinter.New(os.Stdout)
	printer.Print(Repos)
}

// last known status can be success or failure
func SetUnknownsToPrevDay() {
	for i, repo := range Repos {
		lastKnownStatus := UNKNOWN
		lastKnownUrl := ""
		for j, build := range repo.Builds {
			if build.Status == SUCCESS || build.Status == FAILURE {
				lastKnownStatus = build.Status
				lastKnownUrl = build.Url
			}
			if build.Status == UNKNOWN && (lastKnownStatus == FAILURE || lastKnownStatus == SUCCESS) {
				Repos[i].Builds[j].Status = lastKnownStatus
				Repos[i].Builds[j].Url = lastKnownUrl
			}
		}
	}
}

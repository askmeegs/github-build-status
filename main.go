package main

import (
	"fmt"
	"os"
	"time"

	rice "github.com/GeertJohan/go.rice"
	"github.com/foolin/gin-template/supports/gorice"
	"github.com/gin-gonic/gin"
	"github.com/google/go-github/v36/github"
	"github.com/jinzhu/configor"
	log "github.com/sirupsen/logrus"

	"net/http"
)

const (
	SUCCESS    = "success" // green
	FAILURE    = "failure" // red
	PENDING    = "pending" // yellow
	UNKNOWN    = "unknown" // gray
	DAYHISTORY = 7         // currently GBS supports a build history of the last week
)

var (
	// RepoCache is a slice of Repo/Build info for the last 7 days.
	// It's ordered in the way it's defined in config.yml
	Repos []Repo

	// Dates is a slice of formatted dates for the last 7 days, used for the UI
	Dates []string

	// Latest day is the latest day Repos knows about. resets at midnight.
	latestTimestamp *github.Timestamp
)

// A Build represents the latest build result for a specific day, for 1 Github repo.
// A Build's Status is composite, meaning it's an aggregated result of all checks on
// the default branch of that repo. A composite Status will only be "success" if all the
// checks have passed; if a single check is pending or failing, that is what the status will be.
// "unknown" means that no builds have happened on that day. allows for a grayed-out status in the UI.
type Build struct {
	Datefmt   string            `header:"datefmt"` //formatted date from the timestamp: MM/DD/YY
	Timestamp *github.Timestamp `header:"timestamp"`
	Status    string            `header:"status"` // one of: SUCCESS, FAILURE, PENDING, UNKNOWN
	Url       string            `header:"URL"`
	Sha       string            `header:"SHA"` // short github commit sha for this build
}

// A Repo represents metadata + historical builds for a single Github repo.
// A Repo is uniquely keyed with its Github link - this is also the Redis key.
type Repo struct {
	Link          string  `header:"link"`
	Owner         string  `header:"owner"`          //eg. GoogleCloudPlatform
	Name          string  `header:"name"`           // eg. bank-of-anthos
	DefaultBranch string  `header:"default branch"` //eg. main
	Builds        []Build `header:"Builds"`         // slice of builds for the last 7 days
}

// Config is the list of Github Repos the user wants to watch - defined in config.yml
var Config = struct {
	Repos []struct {
		Owner         string `required:"true"`
		Name          string `required:"true"`
		DefaultBranch string `default:"main"`
	}
}{}

// Runs at startup  - Get Config from file
func loadConfig() error {
	err := configor.Load(&Config, "/tmp/sample-config.yml")
	if err != nil {
		return err
	}
	Repos = []Repo{}
	for _, r := range Config.Repos {
		repoStruct := Repo{
			Link:          fmt.Sprintf("https://github.com/%s/%s", r.Owner, r.Name),
			Owner:         r.Owner,
			Name:          r.Name,
			DefaultBranch: r.DefaultBranch,
			Builds:        []Build{},
		}
		Repos = append(Repos, repoStruct)
	}
	return nil
}

// Runs at startup - creates an in-memory build cache of the last 7 days,
// populating it with any historical data from Redis, then fetching the
// latest builds from Github.
func SetUpRepos() error {
	log.Info("Setting up Repos struct and getting from Redis...")
	Dates = []string{}
	now := &github.Timestamp{time.Now()}
	latestTimestamp = now

	// initialize Dates to represent the last 7 days
	for i := 0; i < DAYHISTORY; i++ {
		curDate := now.AddDate(0, 0, -i) // go back X days
		curFmt := GenerateDatefmt(github.Timestamp{curDate})
		Dates = append([]string{curFmt}, Dates...)
	}
	log.Infof("Dates is: %v", Dates)

	// initialize Repos with blank Builds for the last 7 days
	for i := range Repos {
		builds := []Build{}
		for i := 0; i < DAYHISTORY; i++ {
			curDate := now.AddDate(0, 0, -i) // go back X days
			curFmt := GenerateDatefmt(github.Timestamp{curDate})
			blankBuild := Build{
				Timestamp: &github.Timestamp{curDate},
				Status:    UNKNOWN,
				Url:       "",
				Sha:       "",
				Datefmt:   curFmt,
			}
			builds = append([]Build{blankBuild}, builds...)
		}
		Repos[i].Builds = builds
	}
	// Get any persisted data
	err := GetFromRedisAndReconcile()
	if err != nil {
		return err
	}
	// Make an initial set of calls to Github to get the latest builds
	GetLatestGithubBuilds()
	return UpdateRedis()
}

// helper - runs at midnight (any time time.Now is after latestTimestamp)
func TurnoverDate() {
	log.Warn("🗓️ Turning over date...")
	latestTimestamp = &github.Timestamp{time.Now()}
	todaysDate := GenerateDatefmt(*latestTimestamp)

	// reset Dates (remove the oldest, add today)
	Dates = Dates[1:]
	Dates = append(Dates, todaysDate)

	// reset Repo builds (remove the oldest, add an unknown build for Today)
	for i, r := range Repos {
		builds := r.Builds
		if len(builds) == 0 {
			log.Error("Builds for repo %s is empty - cannot reset date", r.Link)
			continue
		}
		// remove the oldest build from repo
		builds = builds[1:]
		// create a new unknown build for Today
		todaysBuild := Build{
			Timestamp: latestTimestamp,
			Status:    UNKNOWN,
			Url:       "",
			Sha:       "",
			Datefmt:   todaysDate,
		}
		builds = append(builds, todaysBuild)
		Repos[i].Builds = builds
	}
	log.Info("📅 Turned over Date - today is now %s", todaysDate)
}

func main() {
	log.Info("😸 Starting Github Build Status server...")

	// read config from config.yml
	err := loadConfig()
	if err != nil {
		log.Errorf("Error loading in config.yml - %v", err)
		os.Exit(1)
	}

	// Set up Github API client
	InitGithubClient(os.Getenv("GITHUB_TOKEN"))

	// Set up Redis client
	InitRedisClient(os.Getenv("REDIS_ADDR"))

	// set up Repos in-memory cache
	err = SetUpRepos()
	if err != nil || len(Repos) == 0 {
		log.Errorf("Error setting up Repos - %v", err)
		os.Exit(1)
	}
	log.Infof("🏁 Set up %d Repos. Ready to query Github", len(Repos))

	// start redis update ticker - every minute
	redisTicker := time.NewTicker(60 * time.Second)
	redisQuit := make(chan struct{})
	go func() {
		for {
			select {
			case <-redisTicker.C:
				UpdateRedis()
			case <-redisQuit:
				redisTicker.Stop()
				return
			}
		}
	}()

	// start github fetch ticker - every 20 seconds
	githubTicker := time.NewTicker(20 * time.Second)
	githubQuit := make(chan struct{})
	go func() {
		for {
			select {
			case <-githubTicker.C:
				GetLatestGithubBuilds()
			case <-githubQuit:
				githubTicker.Stop()
				return
			}
		}
	}()

	// Set up gin webserver. Has a single handler, which renders the build grid as an HTML page.
	r := gin.Default()

	// serve CSS files (bootstrap.css + custom status colors)
	staticBox := rice.MustFindBox("static")
	r.StaticFS("/static", staticBox.HTTPBox())
	r.HTMLRender = gorice.New(rice.MustFindBox("views"))

	// Index.html handler - display build status as a grid.
	r.GET("/", func(c *gin.Context) {
		c.HTML(http.StatusOK, "templates/index.html", gin.H{"dates": Dates, "repos": Repos})
	})

	r.Run() // gin runs on localhost:8080
}

package main

import (
	"fmt"
	"os"
	"time"
	"unicode/utf8"

	"github.com/google/go-github/v36/github"

	"github.com/lensesio/tableprinter"
)

// get first 7 characters of a git commit SHA string
// if string is less than 6 characters, return the unmodified string
func GenerateShortSha(sha string) string {
	if utf8.RuneCountInString(sha) <= 7 {
		return sha
	}
	return sha[:7]
}

// generates the GBS "composite status" from a list of build checks
// (represents the build status for a single commit of a single repo.)
// returns one of: SUCCESS, FAILURE, PENDING, UNKNOWNdat
func GenerateCompositeStatus(checkRuns []*github.CheckRun) string {
	if len(checkRuns) == 0 {
		return UNKNOWN
	}
	for _, checkRun := range checkRuns {
		if *checkRun.Status == "queued" || *checkRun.Status == "in_progress" {
			return PENDING
		} else if *checkRun.Conclusion == FAILURE {
			return FAILURE
		}
	}
	return SUCCESS
}

func SetUnknownsToPrevDay() {
	for i, repo := range Repos {
		lastKnownStatus := UNKNOWN
		lastKnownUrl := ""
		lastKnownSha := ""
		for j, build := range repo.Builds {
			if build.Status == SUCCESS || build.Status == FAILURE {
				lastKnownStatus = build.Status
				lastKnownUrl = build.Url
				lastKnownSha = build.Sha
			}
			if build.Status == UNKNOWN && (lastKnownStatus == FAILURE || lastKnownStatus == SUCCESS) {
				Repos[i].Builds[j].Status = lastKnownStatus
				Repos[i].Builds[j].Url = lastKnownUrl
				Repos[i].Builds[j].Sha = lastKnownSha
			}
		}
	}
}

// helper - given a time.Time, returns a string formatted as MM/DD/YY
func GenerateDatefmt(t github.Timestamp) string {
	y, m, d := t.Date()
	return fmt.Sprintf("%d/%d/%d", m, d, y)
}

// Helper function - returns true iff date2's calendar date is after date1's
func DateAfter(date1, date2 time.Time) bool {
	y1, m1, d1 := date1.Date()
	y2, m2, d2 := date2.Date()

	t1 := time.Date(y1, m1, d1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(y2, m2, d2, 0, 0, 0, 0, time.UTC)

	return t2.After(t1)
}

// helper - pretty-prints what is shown in the UI
func PrintGrid() {
	printer := tableprinter.New(os.Stdout)
	printer.Print(Repos)
}

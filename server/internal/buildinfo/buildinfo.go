// Package buildinfo exposes build metadata injected at link time.
//
// Populated via -ldflags "-X .../buildinfo.Commit=abc123 ...". The site footer
// and /api/build read these so the deploy pipeline is publicly visible.
package buildinfo

import (
	"strconv"
	"time"
)

var (
	// Commit is the full git SHA of the build.
	Commit = "dev"
	// BuiltAt is the build timestamp as a Unix epoch string.
	BuiltAt = "0"
	// BuildSeconds is how long CI took to produce this binary.
	BuildSeconds = "0"
)

// Info is the JSON shape served at /api/build.
type Info struct {
	Commit       string    `json:"commit"`
	ShortCommit  string    `json:"shortCommit"`
	BuiltAt      time.Time `json:"builtAt"`
	BuildSeconds int       `json:"buildSeconds"`
	StartedAt    time.Time `json:"startedAt"`
	Go           string    `json:"go"`
}

var startedAt = time.Now().UTC()

// Get returns the parsed build metadata.
func Get(goVersion string) Info {
	epoch, _ := strconv.ParseInt(BuiltAt, 10, 64)
	secs, _ := strconv.Atoi(BuildSeconds)

	short := Commit
	if len(short) > 7 {
		short = short[:7]
	}

	return Info{
		Commit:       Commit,
		ShortCommit:  short,
		BuiltAt:      time.Unix(epoch, 0).UTC(),
		BuildSeconds: secs,
		StartedAt:    startedAt,
		Go:           goVersion,
	}
}

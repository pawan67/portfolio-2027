// Package listening publishes what the site's owner is currently listening to.
//
// The track comes from Last.fm rather than Spotify. Once the two accounts are
// linked, Last.fm scrobbles whatever Spotify plays, and its read API takes a
// plain API key -- no OAuth dance, no refresh token that expires when a
// password changes and silently blanks the footer. The 30-second sample comes
// from the iTunes Search API, which needs no key at all; Spotify deprecated
// preview_url for new apps in late 2024, so the track source and the audio
// source have to be two different services regardless of which one names the
// track.
//
// Both upstreams are polled by one background loop and proxied through this
// origin. Nothing in the browser talks to either service: the page keeps
// `connect-src 'self'`, the API key never leaves the process, and visitor
// volume has no bearing on how often the upstreams are called.
package listening

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode"
)

// A malformed or hostile upstream response should not be read without bound.
// Last.fm returns a few KB for two tracks; iTunes a few KB for one result.
const maxUpstreamBytes = 256 << 10

// track is the shape the API publishes, and the only shape the browser sees.
type track struct {
	// False means this is the most recent scrobble rather than a live one, so
	// the footer can say "last played" instead of claiming something is on.
	Playing bool   `json:"playing"`
	Title   string `json:"title"`
	Artist  string `json:"artist"`
	Album   string `json:"album,omitempty"`
	URL     string `json:"url,omitempty"`
	// A path on this origin, never the upstream URL. The browser does not learn
	// where the audio really comes from, and `media-src 'self'` holds.
	Preview string `json:"preview,omitempty"`
}

// key identifies a track for preview lookups. Artist and title are what both
// upstreams agree on; Last.fm has no stable ID that survives a re-scrobble.
func (t track) key() string {
	return normalize(t.Artist) + "\x00" + normalize(t.Title)
}

// ---------------------------------------------------------------------------
// Last.fm
// ---------------------------------------------------------------------------

type lastfmClient struct {
	http   *http.Client
	base   string
	user   string
	apiKey string
}

// lastfmResponse covers both outcomes, because Last.fm answers a bad API key
// or an unknown user with HTTP 200 and an error object. Checking the status
// code alone would treat that as a successful empty result and quietly wipe the
// cached track.
type lastfmResponse struct {
	Recent struct {
		// Array-or-object: with a now-playing track Last.fm returns a list, but
		// a user with exactly one scrobble and nothing playing comes back as a
		// bare object. Decoding straight into a slice fails on the second case.
		Track json.RawMessage `json:"track"`
	} `json:"recenttracks"`

	Error   int    `json:"error"`
	Message string `json:"message"`
}

type lastfmTrack struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Artist struct {
		Text string `json:"#text"`
	} `json:"artist"`
	Album struct {
		Text string `json:"#text"`
	} `json:"album"`
	Attr struct {
		NowPlaying string `json:"nowplaying"`
	} `json:"@attr"`
}

// recent returns the newest scrobble, flagged with whether it is playing now.
//
// limit=2 rather than 1: when something is playing, Last.fm prepends the live
// track to the list without counting it against the limit, so limit=1 can
// return two entries. Asking for two makes the array shape the normal case and
// costs nothing.
func (c *lastfmClient) recent(ctx context.Context) (track, error) {
	q := url.Values{
		"method":  {"user.getrecenttracks"},
		"user":    {c.user},
		"api_key": {c.apiKey},
		"format":  {"json"},
		"limit":   {"2"},
	}

	var body lastfmResponse
	if err := c.get(ctx, c.base+"?"+q.Encode(), &body); err != nil {
		return track{}, err
	}
	if body.Error != 0 {
		return track{}, fmt.Errorf("last.fm error %d: %s", body.Error, body.Message)
	}

	first, err := firstTrack(body.Recent.Track)
	if err != nil {
		return track{}, err
	}

	t := track{
		Playing: first.Attr.NowPlaying == "true",
		Title:   strings.TrimSpace(first.Name),
		Artist:  strings.TrimSpace(first.Artist.Text),
		Album:   strings.TrimSpace(first.Album.Text),
		URL:     first.URL,
	}
	if t.Title == "" || t.Artist == "" {
		return track{}, errors.New("last.fm returned a track with no title or artist")
	}
	return t, nil
}

// firstTrack handles the array-or-object ambiguity described above.
func firstTrack(raw json.RawMessage) (lastfmTrack, error) {
	if len(raw) == 0 {
		return lastfmTrack{}, errors.New("last.fm returned no tracks")
	}

	var list []lastfmTrack
	if err := json.Unmarshal(raw, &list); err == nil {
		if len(list) == 0 {
			return lastfmTrack{}, errors.New("last.fm returned an empty track list")
		}
		return list[0], nil
	}

	var single lastfmTrack
	if err := json.Unmarshal(raw, &single); err != nil {
		return lastfmTrack{}, fmt.Errorf("last.fm track was neither list nor object: %w", err)
	}
	return single, nil
}

func (c *lastfmClient) get(ctx context.Context, endpoint string, into any) error {
	return getJSON(ctx, c.http, endpoint, into)
}

// ---------------------------------------------------------------------------
// iTunes Search
// ---------------------------------------------------------------------------

type itunesClient struct {
	http *http.Client
	base string
	// Storefront to search. Catalogues differ by country and so do the tracks
	// that have a preview at all; change this if too many come back empty.
	country string
}

type itunesResponse struct {
	Results []struct {
		TrackName  string `json:"trackName"`
		ArtistName string `json:"artistName"`
		PreviewURL string `json:"previewUrl"`
	} `json:"results"`
}

// preview resolves a 30-second sample URL, or "" when there is no confident
// match. Empty is a normal outcome, not an error: the footer simply shows the
// track without a play control.
//
// Several results are requested rather than one because iTunes ranks fuzzily,
// and the top hit for "Artist Title" is regularly a karaoke or tribute version
// by someone else. Playing the wrong recording on a portfolio is worse than
// playing nothing, so every candidate is checked before it is accepted.
func (c *itunesClient) preview(ctx context.Context, artist, title string) (string, error) {
	q := url.Values{
		"term":    {artist + " " + title},
		"media":   {"music"},
		"entity":  {"song"},
		"limit":   {"5"},
		"country": {c.country},
	}

	var body itunesResponse
	if err := getJSON(ctx, c.http, c.base+"?"+q.Encode(), &body); err != nil {
		return "", err
	}

	for _, r := range body.Results {
		if r.PreviewURL == "" {
			continue
		}
		if matches(r.ArtistName, artist) && matches(r.TrackName, title) {
			return r.PreviewURL, nil
		}
	}
	return "", nil
}

// matches compares two names loosely enough to survive the differences between
// catalogues -- punctuation, case, and the "(feat. ...)" or "- Remastered 2011"
// tails that one service appends and the other does not -- while still refusing
// an outright different recording.
func matches(a, b string) bool {
	na, nb := normalize(a), normalize(b)
	if na == "" || nb == "" {
		return false
	}
	return strings.HasPrefix(na, nb) || strings.HasPrefix(nb, na)
}

// normalize folds a name to lowercase alphanumerics. Spaces go too, so
// "Sigur Ros" and "SigurRos" compare equal.
func normalize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ---------------------------------------------------------------------------

func getJSON(ctx context.Context, client *http.Client, endpoint string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	// Both APIs are public and unauthenticated beyond the query string, but
	// Last.fm asks that clients identify themselves.
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("upstream returned %s", resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, maxUpstreamBytes)).Decode(into)
}

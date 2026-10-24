package cliupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const Repository = "qianlan33333-png/aicrm-cli"
const RepositoryURL = "https://github.com/" + Repository
const CheckTimeout = 1500 * time.Millisecond
const CheckInterval = 6 * time.Hour // Legacy interactive notifier only.

var ErrBusy = errors.New("another CLI update operation is in progress")

type Asset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}
type Release struct {
	Tag        string  `json:"tag_name"`
	Prerelease bool    `json:"prerelease"`
	Draft      bool    `json:"draft"`
	URL        string  `json:"html_url"`
	Assets     []Asset `json:"assets"`
}
type Result struct {
	Current   string    `json:"current_version"`
	Latest    string    `json:"latest_version,omitempty"`
	Channel   string    `json:"channel"`
	Available bool      `json:"update_available"`
	Cached    bool      `json:"cached"`
	CheckedAt time.Time `json:"checked_at,omitempty"`
	Release   Release   `json:"release"`
}
type cache struct {
	AttemptedAt time.Time `json:"attempted_at"`
	CheckedAt   time.Time `json:"checked_at"`
	Release     Release   `json:"release"`
}
type Checker struct {
	Client   *http.Client
	CacheDir string
	Now      func() time.Time
}

func CacheDirectory() (string, error) {
	d, e := os.UserCacheDir()
	if e != nil {
		return "", e
	}
	return filepath.Join(d, "aicrm-cli", "updates"), nil
}
func (c Checker) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}
func releaseClient(client *http.Client, assets bool) *http.Client {
	var h http.Client
	if client != nil {
		h = *client
	}
	h.Jar = nil // Public release checks never borrow login/session cookies.
	h.Timeout = CheckTimeout
	if assets {
		h.Timeout = 2 * time.Minute
	}
	h.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if !assets {
			return http.ErrUseLastResponse
		}
		if len(via) > 3 || req.URL.Scheme != "https" || req.URL.User != nil {
			return errors.New("untrusted release redirect")
		}
		switch req.URL.Host {
		case "release-assets.githubusercontent.com", "objects.githubusercontent.com":
			return nil
		}
		return errors.New("untrusted release redirect")
	}
	return &h
}
func releaseValid(r Release) bool {
	v, e := parseVersion(r.Tag)
	return e == nil && !r.Draft && !r.Prerelease && len(v.pre) == 0 && r.URL == RepositoryURL+"/releases/tag/"+url.PathEscape(r.Tag)
}

func (c Checker) fetch(ctx context.Context) (Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, RepositoryURL+"/releases/latest", nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("User-Agent", "aicrm-cli-update")
	req.Header.Set("Cache-Control", "no-cache")
	resp, err := releaseClient(c.Client, false).Do(req)
	if err != nil {
		return Release{}, errors.New("release metadata unavailable")
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
	default:
		return Release{}, fmt.Errorf("release metadata HTTP %d", resp.StatusCode)
	}
	location, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || resp.Header.Get("Location") == "" {
		return Release{}, errors.New("release identity invalid")
	}
	location = req.URL.ResolveReference(location)
	if location.Scheme != "https" || location.Host != "github.com" || location.User != nil || location.RawQuery != "" || location.ForceQuery || location.Fragment != "" {
		return Release{}, errors.New("release identity invalid")
	}
	r := Release{Tag: strings.TrimPrefix(location.Path, "/"+Repository+"/releases/tag/"), URL: location.String(), Assets: []Asset{}}
	if !releaseValid(r) {
		return Release{}, errors.New("release identity invalid")
	}
	return r, nil
}

// Check serializes automatic checks across processes. Failures are frequency
// limited too; stale success is never represented as a fresh online result.
func (c Checker) Check(ctx context.Context, current, channel string, force bool) (Result, error) {
	r := Result{Current: current, Channel: channel}
	if channel == "prerelease" {
		return r, errors.New("automatic prerelease selection is no longer supported; download and replace it manually from " + RepositoryURL + "/releases; upgrade --target installs stable releases")
	}
	if channel != "stable" {
		return r, errors.New("channel must be stable")
	}
	if _, e := parseVersion(current); e != nil {
		return r, errors.New("development build has no release version")
	}
	d := c.CacheDir
	var e error
	if d == "" {
		d, e = CacheDirectory()
		if e != nil {
			return r, e
		}
	}
	if e = os.MkdirAll(d, 0700); e != nil {
		return r, e
	}
	unlock, e := tryLock(filepath.Join(d, channel+".lock"))
	if e != nil {
		return r, e
	}
	defer unlock()
	p := filepath.Join(d, channel+".json")
	var old cache
	if b, e := os.ReadFile(p); e == nil {
		_ = json.Unmarshal(b, &old)
	}
	now := c.now()
	recent := !old.AttemptedAt.IsZero() && !old.AttemptedAt.After(now) && now.Sub(old.AttemptedAt) < CheckInterval
	if !force && recent {
		if old.CheckedAt.IsZero() || !releaseValid(old.Release) {
			return r, errors.New("release metadata unavailable; retry after cache interval or use update check --refresh")
		}
		r.Cached = true
		r.CheckedAt = old.CheckedAt
		r.Release = old.Release
	} else {
		// No old success is retained after a failed refresh.
		attempt := cache{AttemptedAt: now}
		if e = atomicJSON(p, attempt); e != nil {
			return r, e
		}
		checkCtx, cancel := context.WithTimeout(ctx, CheckTimeout)
		defer cancel()
		latest, e := c.fetch(checkCtx)
		if e != nil {
			return r, e
		}
		attempt.CheckedAt = now
		attempt.Release = latest
		if e = atomicJSON(p, attempt); e != nil {
			return r, e
		}
		r.CheckedAt = now
		r.Release = latest
	}
	// Old cache files may contain REST metadata; never expose or install from it.
	r.Release.Assets = []Asset{}
	r.Latest = r.Release.Tag
	cmp, e := Compare(r.Latest, current)
	if e != nil {
		return r, e
	}
	r.Available = cmp > 0
	return r, nil
}
func atomicJSON(path string, value any) error {
	b, e := json.MarshalIndent(value, "", "  ")
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".cli-update-*")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, e = f.Write(append(b, '\n')); e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	return replaceFile(tmp, path)
}

// Latest checks exactly once without consulting or writing the notifier cache.
func (c Checker) Latest(ctx context.Context, current string) (Result, error) {
	r := Result{Current: current, Channel: "stable"}
	if _, err := parseVersion(current); err != nil {
		return r, errors.New("development build has no release version")
	}
	ctx, cancel := context.WithTimeout(ctx, CheckTimeout)
	defer cancel()
	release, err := c.fetch(ctx)
	if err != nil {
		return r, err
	}
	cmp, err := Compare(release.Tag, current)
	if err != nil {
		return r, err
	}
	r.Release = release
	r.Latest = release.Tag
	r.Available = cmp > 0
	r.CheckedAt = c.now()
	return r, nil
}

package cliupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
const CheckInterval = 6 * time.Hour // at most four automatic API checks/day/client
const metadataLimit = 2 << 20

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
func apiClient(client *http.Client, assets bool) *http.Client {
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
		if len(via) > 3 || !assets || req.URL.Scheme != "https" || req.URL.User != nil {
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
func readJSON(ctx context.Context, h *http.Client, u string, into any) (*http.Response, error) {
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "aicrm-cli-update")
	r, e := h.Do(req)
	if e != nil {
		return nil, errors.New("release metadata unavailable")
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release metadata HTTP %d", r.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(r.Body, metadataLimit+1))
	if e != nil || len(b) > metadataLimit {
		return nil, errors.New("release metadata unreadable")
	}
	if e = json.Unmarshal(b, into); e != nil {
		return nil, errors.New("release metadata invalid")
	}
	return r, nil
}
func releaseValid(r Release, channel string) bool {
	v, e := parseVersion(r.Tag)
	if e != nil || r.Draft || channel == "stable" && (r.Prerelease || len(v.pre) > 0) {
		return false
	}
	return r.URL == RepositoryURL+"/releases/tag/"+url.PathEscape(r.Tag)
}
func (c Checker) fetch(ctx context.Context, channel string) (Release, error) {
	h := apiClient(c.Client, false)
	base := "https://api.github.com/repos/" + Repository + "/releases"
	if channel == "stable" {
		var r Release
		_, e := readJSON(ctx, h, base+"/latest", &r)
		if e != nil {
			return r, e
		}
		if !releaseValid(r, channel) {
			return Release{}, errors.New("release identity invalid")
		}
		return r, nil
	}
	var best Release
	next := base + "?per_page=100"
	seen := map[string]bool{}
	for next != "" {
		if seen[next] {
			return Release{}, errors.New("release pagination repeated")
		}
		seen[next] = true
		var items []Release
		resp, e := readJSON(ctx, h, next, &items)
		if e != nil {
			return Release{}, e
		}
		for _, r := range items {
			if !releaseValid(r, channel) {
				continue
			}
			if best.Tag == "" {
				best = r
			} else if cmp, _ := Compare(r.Tag, best.Tag); cmp > 0 {
				best = r
			}
		}
		next = ""
		for _, part := range strings.Split(resp.Header.Get("Link"), ",") {
			if !strings.Contains(part, `rel="next"`) {
				continue
			}
			start, end := strings.IndexByte(part, '<'), strings.IndexByte(part, '>')
			if start < 0 || end <= start {
				return Release{}, errors.New("release pagination invalid")
			}
			candidate := part[start+1 : end]
			u, e := url.Parse(candidate)
			if e != nil || u.Scheme != "https" || u.Host != "api.github.com" || u.User != nil || u.Path != "/repos/"+Repository+"/releases" || u.Fragment != "" {
				return Release{}, errors.New("release pagination untrusted")
			}
			next = candidate
		}
	}
	if best.Tag == "" {
		return Release{}, errors.New("no valid published release")
	}
	return best, nil
}

// Check serializes automatic checks across processes. Failures are frequency
// limited too; stale success is never represented as a fresh online result.
func (c Checker) Check(ctx context.Context, current, channel string, force bool) (Result, error) {
	r := Result{Current: current, Channel: channel}
	if channel != "stable" && channel != "prerelease" {
		return r, errors.New("channel must be stable or prerelease")
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
		if old.CheckedAt.IsZero() || !releaseValid(old.Release, channel) {
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
		latest, e := c.fetch(checkCtx, channel)
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

package cliupdate

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(value any) *http.Response {
	b, _ := json.Marshal(value)
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(b)))}
}
func release(tag string) Release {
	return Release{Tag: tag, URL: RepositoryURL + "/releases/tag/" + tag}
}
func latestResponse(tag string) *http.Response {
	r := response(nil)
	r.StatusCode = http.StatusFound
	r.Header.Set("Location", release(tag).URL)
	return r
}
func publicHEAD(t *testing.T, r *http.Request) {
	t.Helper()
	if r.Method != http.MethodHead || r.URL.String() != RepositoryURL+"/releases/latest" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("unexpected public version request: %s %s", r.Method, r.URL)
	}
}
func TestCheckStableCacheAndOfflineIsolation(t *testing.T) {
	var calls atomic.Int32
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(RepositoryURL)
	jar.SetCookies(u, []*http.Cookie{{Name: "private", Value: "must-not-escape"}})
	c := Checker{CacheDir: t.TempDir(), Client: &http.Client{Jar: jar, Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		publicHEAD(t, r)
		return latestResponse("v1.10.0"), nil
	})}}
	r, e := c.Check(context.Background(), "1.9.0", "stable", false)
	if e != nil || !r.Available || r.Cached || r.Release.Assets == nil || len(r.Release.Assets) != 0 {
		t.Fatalf("online result %+v %v", r, e)
	}
	r, e = c.Check(context.Background(), "1.10.0", "stable", false)
	if e != nil || r.Available || !r.Cached || calls.Load() != 1 {
		t.Fatalf("cache %+v %v", r, e)
	}
	c.Client = &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("offline secret-url-must-not-escape")
	})}
	_, e = c.Check(context.Background(), "1.9.0", "stable", true)
	if e == nil || strings.Contains(e.Error(), "secret-url") {
		t.Fatalf("offline disclosure: %v", e)
	}
	_, e = c.Check(context.Background(), "1.9.0", "stable", false)
	if e == nil || calls.Load() != 2 {
		t.Fatal("offline check retried or used stale success")
	}
}
func TestCheckPrereleaseRejectedBeforeIOAndCache(t *testing.T) {
	parent := t.TempDir()
	for _, existing := range []bool{false, true} {
		dir := filepath.Join(parent, map[bool]string{false: "absent", true: "cached"}[existing])
		if existing {
			if e := os.MkdirAll(dir, 0700); e != nil {
				t.Fatal(e)
			}
			if e := atomicJSON(filepath.Join(dir, "prerelease.json"), cache{AttemptedAt: time.Now(), CheckedAt: time.Now(), Release: release("v99.0.0-rc.1")}); e != nil {
				t.Fatal(e)
			}
		}
		c := Checker{CacheDir: dir, Client: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { t.Fatal("disabled channel used network"); return nil, nil })}}
		for _, force := range []bool{false, true} {
			if _, e := c.Check(context.Background(), "1.0.0", "prerelease", force); e == nil || !strings.Contains(e.Error(), "no longer supported") {
				t.Fatalf("prerelease result %v", e)
			}
		}
		if !existing {
			if _, e := os.Stat(dir); !os.IsNotExist(e) {
				t.Fatal("disabled channel touched filesystem")
			}
		}
	}
}
func TestCheckConcurrentAndCancellation(t *testing.T) {
	var calls atomic.Int32
	entered, gate := make(chan struct{}), make(chan struct{})
	c := Checker{CacheDir: t.TempDir(), Client: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		publicHEAD(t, r)
		if calls.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-gate:
			return latestResponse("v1.1.0"), nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})}}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, e := c.Check(context.Background(), "1.0.0", "stable", false); e != nil {
			t.Error(e)
		}
	}()
	<-entered
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := c.Check(context.Background(), "1.0.0", "stable", false)
			if e != nil && !errors.Is(e, ErrBusy) {
				t.Error(e)
			}
		}()
	}
	close(gate)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("concurrent checks=%d", calls.Load())
	}
	c = Checker{CacheDir: t.TempDir(), Client: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if _, e := c.Check(ctx, "1.0.0", "stable", true); e == nil || time.Since(start) > time.Second {
		t.Fatal("cancellation not bounded")
	}
}
func TestLatestRejectsInvalidPublicResponseWithoutFallback(t *testing.T) {
	for _, tc := range []struct {
		name, location string
		status         int
	}{
		{"missing", "", 302}, {"html", release("v2.0.0").URL, 200}, {"forbidden", "", 403}, {"limited", "", 429}, {"not_modified", "", 304},
		{"foreign", "https://evil.example/release", 302}, {"api", "https://api.github.com/repos/" + Repository + "/releases/latest", 302},
		{"http", strings.Replace(release("v2.0.0").URL, "https:", "http:", 1), 302},
		{"other_repo", "https://github.com/another/repo/releases/tag/v2.0.0", 302},
		{"credentials", "https://private@github.com/" + Repository + "/releases/tag/v2.0.0", 302},
		{"query", release("v2.0.0").URL + "?x=secret", 302}, {"fragment", release("v2.0.0").URL + "#x", 302},
		{"prerelease", release("v2.0.0-rc.1").URL, 302}, {"invalid", release("future").URL, 302},
		{"extra_path", release("v2.0.0").URL + "/extra", 302}, {"encoded_path", RepositoryURL + "/releases/tag/v2.0.0%2fextra", 302},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := Checker{Client: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				publicHEAD(t, r)
				rsp := response(nil)
				rsp.StatusCode = tc.status
				rsp.Header.Set("Location", tc.location)
				return rsp, nil
			})}}
			if _, e := c.Latest(context.Background(), "1.0.0"); e == nil || calls != 1 {
				t.Fatalf("accepted or followed invalid response %v calls=%d", e, calls)
			}
		})
	}
}
func TestLatestAcceptsTrustedRelativeRedirect(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		c := Checker{Client: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			publicHEAD(t, r)
			rsp := latestResponse("v2.0.0")
			rsp.StatusCode = status
			rsp.Header.Set("Location", "/"+Repository+"/releases/tag/v2.0.0")
			return rsp, nil
		})}}
		if r, e := c.Latest(context.Background(), "1.0.0"); e != nil || r.Latest != "v2.0.0" {
			t.Fatalf("trusted redirect %+v %v", r, e)
		}
	}
}
func TestLatestAlwaysChecksOnceWithoutCache(t *testing.T) {
	calls := 0
	c := Checker{CacheDir: filepath.Join(t.TempDir(), "unused"), Client: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		publicHEAD(t, r)
		return latestResponse("v1.2.0"), nil
	})}}
	for _, current := range []string{"1.0.0", "1.2.0", "2.0.0", "2.0.0-rc.1"} {
		r, e := c.Latest(context.Background(), current)
		if e != nil || r.Cached || r.Available != (current == "1.0.0") {
			t.Fatalf("latest %+v %v", r, e)
		}
	}
	if calls != 4 {
		t.Fatalf("queries=%d", calls)
	}
	if _, e := os.Stat(c.CacheDir); !os.IsNotExist(e) {
		t.Fatal("fresh check used cache")
	}
	c.Client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	start := time.Now()
	if _, e := c.Latest(context.Background(), "1.0.0"); e == nil || time.Since(start) > 2*time.Second || calls != 5 {
		t.Fatal("lookup unbounded, retried or cached")
	}
}
func TestLegacyCacheDoesNotExposeRESTAssets(t *testing.T) {
	dir := t.TempDir()
	r := release("v2.0.0")
	r.Assets = []Asset{{Name: "legacy", URL: "https://api.github.com/private"}}
	if e := atomicJSON(filepath.Join(dir, "stable.json"), cache{AttemptedAt: time.Now(), CheckedAt: time.Now(), Release: r}); e != nil {
		t.Fatal(e)
	}
	c := Checker{CacheDir: dir, Client: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { t.Fatal("legacy cache refreshed"); return nil, nil })}}
	got, e := c.Check(context.Background(), "1.0.0", "stable", false)
	if e != nil || !got.Cached || got.Release.Assets == nil || len(got.Release.Assets) != 0 {
		t.Fatalf("legacy cache %+v %v", got, e)
	}
}

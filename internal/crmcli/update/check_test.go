package cliupdate

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
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
func TestCheckStableCacheAndOfflineIsolation(t *testing.T) {
	var calls atomic.Int32
	c := Checker{CacheDir: t.TempDir(), Client: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if r.URL.String() != "https://api.github.com/repos/"+Repository+"/releases/latest" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("release check leaked business authentication or changed source")
		}
		return response(release("v1.10.0")), nil
	})}}
	r, e := c.Check(context.Background(), "1.9.0", "stable", false)
	if e != nil || !r.Available || r.Cached {
		t.Fatalf("online result %+v %v", r, e)
	}
	r, e = c.Check(context.Background(), "1.10.0", "stable", false)
	if e != nil || r.Available || !r.Cached || calls.Load() != 1 {
		t.Fatalf("cache did not recompute installed version: %+v %v", r, e)
	}
	c.Client = &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("offline secret-url-must-not-escape")
	})}
	_, e = c.Check(context.Background(), "1.9.0", "stable", true)
	if e == nil || strings.Contains(e.Error(), "secret-url") {
		t.Fatalf("offline error disclosure: %v", e)
	}
	_, e = c.Check(context.Background(), "1.9.0", "stable", false)
	if e == nil || calls.Load() != 2 {
		t.Fatal("offline check retried or represented old success as fresh")
	}
}
func TestCheckPrereleaseUsesSemVerAndTrustedPagination(t *testing.T) {
	c := Checker{CacheDir: t.TempDir(), Client: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("page") == "2" {
			return response([]Release{release("v2.0.0-rc.11"), release("v1.99.0")}), nil
		}
		a := release("v2.0.0-rc.2")
		a.Prerelease = true
		draft := release("v99.0.0")
		draft.Draft = true
		bad := release("v100.0.0")
		bad.URL = "https://evil.example/release"
		rsp := response([]Release{release("v1.9.0"), a, draft, bad})
		rsp.Header.Set("Link", `<https://api.github.com/repos/`+Repository+`/releases?per_page=100&page=2>; rel="next"`)
		return rsp, nil
	})}}
	r, e := c.Check(context.Background(), "2.0.0-rc.1", "prerelease", true)
	if e != nil || !r.Available || r.Latest != "v2.0.0-rc.11" {
		t.Fatalf("wrong prerelease: %+v %v", r, e)
	}
	c.Client = &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		rsp := response([]Release{release("v1.0.0")})
		rsp.Header.Set("Link", `<https://evil.example/releases>; rel="next"`)
		return rsp, nil
	})}
	if _, e = c.Check(context.Background(), "1.0.0", "prerelease", true); e == nil {
		t.Fatal("accepted untrusted pagination")
	}
}
func TestCheckConcurrentAndCancellation(t *testing.T) {
	var calls atomic.Int32
	entered, gate := make(chan struct{}), make(chan struct{})
	c := Checker{CacheDir: t.TempDir(), Client: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-gate:
			return response(release("v1.1.0")), nil
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
func TestStableRejectsPrereleaseDraftAndRedirect(t *testing.T) {
	for _, r := range []Release{{Tag: "v2.0.0-rc.1", URL: RepositoryURL + "/releases/tag/v2.0.0-rc.1"}, {Tag: "v2.0.0", Prerelease: true, URL: RepositoryURL + "/releases/tag/v2.0.0"}, {Tag: "v2.0.0", Draft: true, URL: RepositoryURL + "/releases/tag/v2.0.0"}} {
		c := Checker{CacheDir: t.TempDir(), Client: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { return response(r), nil })}}
		if _, e := c.Check(context.Background(), "1.0.0", "stable", true); e == nil {
			t.Fatal("stable accepted unpublished/prerelease")
		}
	}
	var calls int
	c := Checker{CacheDir: t.TempDir(), Client: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		rsp := response(nil)
		rsp.StatusCode = 302
		rsp.Header.Set("Location", "https://evil.example/metadata")
		return rsp, nil
	})}}
	if _, e := c.Check(context.Background(), "1.0.0", "stable", true); e == nil || calls != 1 {
		t.Fatal("metadata followed redirect")
	}
}

func TestLatestAlwaysChecksOnceWithoutCache(t *testing.T) {
	calls := 0
	c := Checker{CacheDir: t.TempDir(), Client: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Fatal("CRM credential leaked")
		}
		return response(release("v1.2.0")), nil
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
	c.Client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	start := time.Now()
	if _, e := c.Latest(context.Background(), "1.0.0"); e == nil || time.Since(start) > 2*time.Second || calls != 5 {
		t.Fatal("fresh lookup unbounded, retried or cached")
	}
}

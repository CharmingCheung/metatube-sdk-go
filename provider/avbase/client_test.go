package avbase

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/metatube-community/metatube-sdk-go/internal/envconfig"
)

func fixtureProvider(t *testing.T, handler http.HandlerFunc) *AVBase {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	ab := New()
	ab.pages.base = server.URL + "/"
	ab.pages.interval = 0
	ab.pages.timeout = time.Second
	return ab
}

func writePage(w http.ResponseWriter, props string) {
	fmt.Fprintf(w, `<html><script id="__NEXT_DATA__" type="application/json">{"buildId":"fixture-build","props":{"pageProps":%s}}</script></html>`, props)
}

func TestPageFailuresAreNotEmptySearches(t *testing.T) {
	for name, page := range map[string]string{
		"challenge HTML": `<html>Just a moment...</html>`,
		"invalid JSON":   `<script id="__NEXT_DATA__">{</script>`,
		"missing props":  `<script id="__NEXT_DATA__">{}</script>`,
		"missing works":  `<script id="__NEXT_DATA__">{"props":{"pageProps":{}}}</script>`,
		"null works":     `<script id="__NEXT_DATA__">{"props":{"pageProps":{"works":null}}}</script>`,
	} {
		t.Run(name, func(t *testing.T) {
			ab := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, page) })
			_, err := ab.SearchMovie("TEST-001")
			require.Error(t, err)
		})
	}
}

func TestEmptySearchAndRecovery(t *testing.T) {
	var requests int
	ab := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			fmt.Fprint(w, `<html>unavailable</html>`)
			return
		}
		writePage(w, `{"works":[]}`)
	})
	_, err := ab.SearchMovie("TEST-001")
	require.Error(t, err)
	results, err := ab.SearchMovie("TEST-001")
	require.NoError(t, err)
	require.Empty(t, results)
	require.Equal(t, 2, requests)
}

func TestForbiddenCooldown(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusOK} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var requests int
			ab := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
				requests++
				w.Header().Set("Cf-Mitigated", "challenge")
				w.WriteHeader(status)
			})
			_, err := ab.SearchMovie("TEST-001")
			var blocked *HTTPError
			require.ErrorAs(t, err, &blocked)
			require.Equal(t, 403, blocked.StatusCode)
			_, err = ab.GetMovieInfoByID("TEST-001")
			require.ErrorAs(t, err, &blocked)
			require.Equal(t, 1, requests)
			ab.pages.cooldown.RetryAt = time.Now().Add(-time.Second)
			_, err = ab.SearchMovie("TEST-001")
			require.Error(t, err)
			require.Equal(t, 2, requests)
		})
	}
}

func TestRetryAfterPersistsAcrossCalls(t *testing.T) {
	for _, header := range []string{"120", time.Now().Add(2 * time.Minute).UTC().Format(http.TimeFormat)} {
		t.Run(header, func(t *testing.T) {
			requests := 0
			ab := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
				requests++
				w.Header().Set("Retry-After", header)
				w.WriteHeader(http.StatusTooManyRequests)
			})
			_, err := ab.SearchMovie("TEST-001")
			var limited *HTTPError
			require.ErrorAs(t, err, &limited)
			require.Equal(t, 429, limited.StatusCode)
			require.Greater(t, time.Until(limited.RetryAt), time.Minute)
			_, err = ab.SearchMovie("TEST-002")
			require.Error(t, err)
			require.Equal(t, 1, requests)
		})
	}
}

func TestTransientRetryRecovers(t *testing.T) {
	requests := 0
	ab := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writePage(w, `{"works":[]}`)
	})
	ab.pages.timeout = 8 * time.Second
	_, err := ab.SearchMovie("TEST-001")
	require.NoError(t, err)
	require.Equal(t, 2, requests)
}

func TestNoRetryForNotFoundOrDisabledRetries(t *testing.T) {
	for _, status := range []int{404, 500, 502, 503, 504} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			requests := 0
			ab := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) { requests++; w.WriteHeader(status) })
			ab.pages.retries = 0
			_, err := ab.SearchMovie("TEST-001")
			require.Error(t, err)
			require.Equal(t, 1, requests)
		})
	}
}

func TestPageRequestsArePacedAndSerialized(t *testing.T) {
	var times []time.Time
	var mu sync.Mutex
	var active, peak atomic.Int32
	ab := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
		current := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); current > old; old = peak.Load() {
			if peak.CompareAndSwap(old, current) {
				break
			}
		}
		mu.Lock()
		times = append(times, time.Now())
		mu.Unlock()
		time.Sleep(5 * time.Millisecond)
		writePage(w, `{"works":[]}`)
	})
	ab.pages.interval = 25 * time.Millisecond
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := ab.SearchMovie("TEST-001"); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, peak.Load())
	require.Len(t, times, 3)
	for i := 1; i < len(times); i++ {
		require.GreaterOrEqual(t, times[i].Sub(times[i-1]), ab.pages.interval)
	}
}

func TestQueueWaitHasDeadline(t *testing.T) {
	ab := New()
	ab.pages.timeout = 20 * time.Millisecond
	ab.pages.gate <- struct{}{}
	_, err := ab.SearchMovie("TEST-001")
	require.Error(t, err)
	<-ab.pages.gate
}

func TestConfigurationAndProxy(t *testing.T) {
	ab := New()
	cfg := envconfig.NewConfig()
	cfg.Set("request_interval", "10s")
	cfg.Set("blocked_cooldown", "10m")
	cfg.Set("max_retries", "1")
	cfg.Set("source_enrichment", "true")
	require.NoError(t, ab.SetConfig(cfg))
	require.Equal(t, 10*time.Second, ab.pages.interval)
	require.True(t, ab.sourceEnrichment)
	for key, value := range map[string]string{"request_interval": "0s", "blocked_cooldown": "-1s", "max_retries": "100", "source_enrichment": "maybe"} {
		invalid := envconfig.NewConfig()
		invalid.Set(key, value)
		require.Error(t, ab.SetConfig(invalid))
	}
	require.Error(t, ab.SetProxy("file:///tmp/proxy"))
	var got string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r.URL.Host; writePage(w, `{"works":[]}`) }))
	defer proxy.Close()
	require.NoError(t, ab.SetProxy(proxy.URL))
	ab.pages.base = "http://avbase.invalid/"
	_, err := ab.SearchMovie("TEST-001")
	require.NoError(t, err)
	require.Equal(t, "avbase.invalid", got)
}

func TestMetadataTLSVerification(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writePage(w, `{"works":[]}`) }))
	defer server.Close()
	ab := New()
	ab.pages.base = server.URL + "/"
	_, err := ab.SearchMovie("TEST-001")
	require.Error(t, err)
}

func TestResponseBoundAndMalformedRetryAfter(t *testing.T) {
	ab := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(make([]byte, maxPageBytes+1)) })
	_, err := ab.SearchMovie("TEST-001")
	require.ErrorContains(t, err, "4 MiB")
	for _, value := range []string{"", "no", "-1", "9999999999999999999999999999"} {
		require.True(t, retryAfter(value, time.Now()).IsZero())
	}
}

func TestBuildIDFailureIsNotCached(t *testing.T) {
	count := 0
	ab := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
		count++
		if count == 1 {
			fmt.Fprint(w, "unavailable")
			return
		}
		writePage(w, `{}`)
	})
	_, err := ab.GetBuildID()
	require.Error(t, err)
	id, err := ab.GetBuildID()
	require.NoError(t, err)
	require.Equal(t, "fixture-build", id)
}

package avbase

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/metatube-community/metatube-sdk-go/model"
	"github.com/metatube-community/metatube-sdk-go/provider"
)

// Synthetic metadata with the field shapes observed on AVBASE in October 2026.
const fixtureWork = `{"prefix":"studio","work_id":"TEST-001","title":"Fixture title","min_date":"Thu Apr 13 2017 09:00:00 GMT+0900 (Japan Standard Time)","actors":[{"name":"Canonical Actor"}],"casts":[{"actor":{"name":"Canonical Actor"}}],"genres":[{"name":"Fixture genre"}],"products":[{"source":"unknown-store","title":"Product title","image_url":"https://images.example/cover.jpg","thumbnail_url":"https://images.example/thumb.jpg","date":"2017-04-13","maker":{"name":"Fixture studio"},"iteminfo":{"director":"Fixture director","description":"Fixture summary","volume":"120"},"sample_image_urls":[{"l":"https://images.example/sample.jpg"}]}]}`

func TestSearchAndDetailUseOnePageAndPreserveIdentity(t *testing.T) {
	var paths []string
	ab := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		require.NotEmpty(t, r.UserAgent())
		require.NotEmpty(t, r.Header.Get("Referer"))
		if r.URL.Path == "/works" {
			require.Equal(t, "TEST-001 & query", r.URL.Query().Get("q"))
			writePage(w, `{"works":[`+fixtureWork+`]}`)
			return
		}
		writePage(w, `{"work":`+fixtureWork+`}`)
	})
	results, err := ab.SearchMovie("TEST-001 & query")
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "studio:TEST-001", results[0].ID)
	require.Equal(t, "https://www.avbase.net/works/studio:TEST-001", results[0].Homepage)
	// Unknown external store still provides usable AVBASE metadata.
	require.Equal(t, []string{"Canonical Actor"}, []string(results[0].Actors))
	info, err := ab.GetMovieInfoByID("TEST-001")
	require.NoError(t, err)
	require.Equal(t, "studio:TEST-001", info.ID)
	require.Equal(t, Name, info.Provider)
	require.Equal(t, "Fixture director", info.Director)
	require.Equal(t, 120, info.Runtime)
	require.Equal(t, "Fixture summary", info.Summary)
	require.Len(t, info.PreviewImages, 1)
	require.Equal(t, 2017, time.Time(info.ReleaseDate).Year())
	require.Len(t, paths, 2)
	require.Equal(t, "/works/TEST-001", paths[1])
}

type sourceSpy struct {
	*AVBase
	calls int
	fail  bool
}

func (s *sourceSpy) GetMovieInfoByID(string) (*model.MovieInfo, error) {
	s.calls++
	if s.fail {
		return nil, fmt.Errorf("upstream unavailable")
	}
	return &model.MovieInfo{ID: "foreign-id", Provider: "Source", Homepage: "https://example.com", Number: "foreign-number", Title: "Source title", CoverURL: "https://example.com/cover"}, nil
}

func TestSourceEnrichmentIsOptionalAndKeepsAVBaseIdentity(t *testing.T) {
	ab := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) { writePage(w, `{"work":`+fixtureWork+`}`) })
	spy := &sourceSpy{AVBase: New()}
	ab.providers = map[string]provider.MovieProvider{"unknown-store": spy}
	info, err := ab.GetMovieInfoByID("TEST-001")
	require.NoError(t, err)
	require.Equal(t, 0, spy.calls)
	require.Equal(t, "Fixture title", info.Title)
	ab.sourceEnrichment = true
	info, err = ab.GetMovieInfoByID("TEST-001")
	require.NoError(t, err)
	require.Equal(t, 1, spy.calls)
	require.Equal(t, "Source title", info.Title)
	require.Equal(t, "studio:TEST-001", info.ID)
	require.Equal(t, "TEST-001", info.Number)
	require.Equal(t, Name, info.Provider)
	require.Equal(t, []string{"Canonical Actor"}, []string(info.Actors))
	spy.fail = true
	info, err = ab.GetMovieInfoByID("TEST-001")
	require.NoError(t, err)
	require.Equal(t, "Fixture title", info.Title)
}

func TestInvalidDetailDoesNotSucceed(t *testing.T) {
	for _, props := range []string{`{}`, `{"work":null}`, `{"work":{}}`, `{"work":{"work_id":"TEST-001","title":"no cover"}}`} {
		ab := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) { writePage(w, props) })
		info, err := ab.GetMovieInfoByID("TEST-001")
		require.Error(t, err)
		require.Nil(t, info)
	}
}

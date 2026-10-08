package engine

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/metatube-community/metatube-sdk-go/collection/maps"
	"github.com/metatube-community/metatube-sdk-go/internal/envconfig"
	"github.com/metatube-community/metatube-sdk-go/model"
	"github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/provider/avbase"
)

func TestAVBaseAvailableInStandardBuildAndConfigurable(t *testing.T) {
	cfg := envconfig.NewConfig()
	cfg.Set("priority", "1010")
	cfg.Set("request_interval", "5s")
	engine := New(nil, WithMovieProviderConfig("AVBASE", cfg))
	require.True(t, engine.IsMovieProvider("AVBASE"))
	require.Equal(t, float64(1010), engine.GetMovieProviders()["AVBASE"].Priority())
	cfg.Set("priority", "0")
	disabled := New(nil, WithMovieProviderConfig("AVBASE", cfg))
	require.False(t, disabled.IsMovieProvider("AVBASE"))
}

// An auto search must not convert a temporarily blocked source into "not found".
type coolingMovieProvider struct {
	provider.MovieProvider
	retry   error
	results []*model.MovieSearchResult
}

func (p *coolingMovieProvider) NormalizeMovieKeyword(s string) string { return s }
func (p *coolingMovieProvider) SearchMovie(string) ([]*model.MovieSearchResult, error) {
	return p.results, p.retry
}

func TestAutoSearchPropagatesCooldownOnlyWithoutResults(t *testing.T) {
	e := New(nil)
	e.movieProviders = maps.NewCaseInsensitiveMap[provider.MovieProvider]()
	source := avbase.New()
	cooldown := &avbase.HTTPError{StatusCode: http.StatusForbidden, RetryAt: time.Now().Add(time.Minute)}
	p := &coolingMovieProvider{MovieProvider: source, retry: cooldown}
	e.movieProviders.Set(source.Name(), p)
	_, err := e.SearchMovieAll("TEST-001", false)
	require.ErrorIs(t, err, cooldown)
	other := &coolingMovieProvider{MovieProvider: source, results: []*model.MovieSearchResult{{ID: "TEST-001", Number: "TEST-001", Provider: source.Name(), Title: "Fixture", Homepage: "https://example.com"}}}
	e.movieProviders.Set("other", other)
	result, err := e.SearchMovieAll("TEST-001", false)
	require.NoError(t, err)
	require.NotEmpty(t, result)
}

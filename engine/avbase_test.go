package engine

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/metatube-community/metatube-sdk-go/internal/envconfig"
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

package route

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/metatube-community/metatube-sdk-go/provider/avbase"
)

func TestCooldownHTTPContract(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	err := &avbase.HTTPError{StatusCode: http.StatusForbidden, RetryAt: time.Now().Add(5 * time.Minute)}
	abortWithError(ctx, fmt.Errorf("wrapped: %w", err))
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	seconds, parseErr := strconv.Atoi(recorder.Header().Get("Retry-After"))
	require.NoError(t, parseErr)
	require.GreaterOrEqual(t, seconds, 299)
	require.LessOrEqual(t, seconds, 300)
	require.Contains(t, recorder.Body.String(), `"retry_at"`)
	require.NotContains(t, recorder.Body.String(), "wrapped")
}

func TestExpiredCooldownKeepsOrdinaryError(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	abortWithError(ctx, &avbase.HTTPError{StatusCode: http.StatusForbidden, RetryAt: time.Now().Add(-time.Minute)})
	require.Empty(t, recorder.Header().Get("Retry-After"))
	require.Equal(t, http.StatusInternalServerError, recorder.Code)
}

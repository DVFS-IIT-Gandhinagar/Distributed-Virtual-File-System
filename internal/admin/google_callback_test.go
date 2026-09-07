package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminGoogleLoginCallback(t *testing.T) {
	t.Setenv("DVFS_AUTH_MOCK", "true")

	server := NewAdminServer("", "")

	// 1. Successful callback with mock code
	req := httptest.NewRequest(http.MethodGet, "/logincallback?code=mock-code:adminuser@gmail.com", nil)
	rr := httptest.NewRecorder()

	server.handleGoogleLoginCallback(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	assert.Contains(t, body, "adminuser@gmail.com")
	assert.Contains(t, body, "Google Authentication Successful")
	assert.Contains(t, body, "Copy Token to Clipboard")

	// 2. Callback with error parameter
	reqErr := httptest.NewRequest(http.MethodGet, "/logincallback?error=access_denied", nil)
	rrErr := httptest.NewRecorder()

	server.handleGoogleLoginCallback(rrErr, reqErr)
	require.Equal(t, http.StatusOK, rrErr.Code)
	bodyErr := rrErr.Body.String()
	assert.Contains(t, bodyErr, "Authentication Failed")
	assert.Contains(t, bodyErr, "access_denied")

	// 3. Callback with missing code
	reqMissing := httptest.NewRequest(http.MethodGet, "/logincallback", nil)
	rrMissing := httptest.NewRecorder()

	server.handleGoogleLoginCallback(rrMissing, reqMissing)
	require.Equal(t, http.StatusOK, rrMissing.Code)
	bodyMissing := rrMissing.Body.String()
	assert.Contains(t, bodyMissing, "Authentication Failed")
	assert.True(t, strings.Contains(bodyMissing, "No authorization code") || strings.Contains(bodyMissing, "Failed"))
}

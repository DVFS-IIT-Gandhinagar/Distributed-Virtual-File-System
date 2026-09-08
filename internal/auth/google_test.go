package auth

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateAuthURL(t *testing.T) {
	cfg := &GoogleDesktopConfig{
		ClientID:    "test-client-id.apps.googleusercontent.com",
		RedirectURI: "http://localhost:38485/logincallback",
	}

	urlStr := GenerateAuthURL(cfg, "alice@example.com", "state-12345")
	assert.Contains(t, urlStr, GoogleAuthEndpoint)
	assert.Contains(t, urlStr, "client_id=test-client-id.apps.googleusercontent.com")
	assert.Contains(t, urlStr, "redirect_uri=http%3A%2F%2Flocalhost%3A38485%2Flogincallback")
	assert.Contains(t, urlStr, "login_hint=alice%40example.com")
	assert.Contains(t, urlStr, "state=state-12345")
	assert.Contains(t, urlStr, "response_type=code")
	assert.Contains(t, urlStr, "scope=openid+email+profile")
}

func TestMockTokenAndVerification(t *testing.T) {
	t.Setenv("DVFS_AUTH_MOCK", "true")

	email := "bob@gmail.com"
	tok := MockToken(email)
	require.NotEmpty(t, tok)

	// Valid email match
	claims, err := VerifyToken(context.Background(), tok, "bob@gmail.com", "mock-client-id")
	require.NoError(t, err)
	assert.Equal(t, "bob@gmail.com", claims.Email)
	assert.True(t, claims.EmailVerified)
	assert.True(t, claims.Expiry > time.Now().Unix())

	// Case-insensitive email match
	claimsUpper, err := VerifyToken(context.Background(), tok, "BOB@GMAIL.COM", "")
	require.NoError(t, err)
	assert.Equal(t, "bob@gmail.com", claimsUpper.Email)

	// Email mismatch rejection
	_, errMismatch := VerifyToken(context.Background(), tok, "charlie@gmail.com", "")
	assert.Error(t, errMismatch)
	assert.Contains(t, errMismatch.Error(), "does not match")

	// Empty token rejection
	_, errEmpty := VerifyToken(context.Background(), "", "bob@gmail.com", "")
	assert.Error(t, errEmpty)
}

func TestExtractEmailFromToken(t *testing.T) {
	tok := MockToken("charlie@example.com")
	email := ExtractEmailFromToken(tok)
	assert.Equal(t, "charlie@example.com", email)

	// Invalid token returns empty string
	assert.Empty(t, ExtractEmailFromToken("invalid-token"))
}

func TestExchangeCodeForTokensMock(t *testing.T) {
	cfg := &GoogleDesktopConfig{
		ClientID:    "mock-id",
		RedirectURI: "http://localhost:38485/logincallback",
		MockAuth:    true,
	}

	tokResp, err := ExchangeCodeForTokens(context.Background(), cfg, "mock-code:alice@test.com", "")
	require.NoError(t, err)
	assert.NotEmpty(t, tokResp.IdToken)
	assert.Equal(t, "Bearer", tokResp.TokenType)

	email := ExtractEmailFromToken(tokResp.IdToken)
	assert.Equal(t, "alice@test.com", email)
}

func TestRenderCallbackHTML(t *testing.T) {
	var buf bytes.Buffer
	err := RenderCallbackHTML(&buf, "alice@gmail.com", "sample-token-123", "")
	require.NoError(t, err)
	html := buf.String()
	assert.Contains(t, html, "Google Authentication Successful")
	assert.Contains(t, html, "alice@gmail.com")
	assert.Contains(t, html, "sample-token-123")
	assert.Contains(t, html, "Copy Token to Clipboard")

	// Test error page rendering
	buf.Reset()
	err = RenderCallbackHTML(&buf, "", "", "Invalid authorization grant")
	require.NoError(t, err)
	errHtml := buf.String()
	assert.Contains(t, errHtml, "Authentication Failed")
	assert.Contains(t, errHtml, "Invalid authorization grant")
}

func TestStartLocalCallbackServer(t *testing.T) {
	cfg := &GoogleDesktopConfig{
		ClientID:    "mock-id",
		RedirectURI: "http://localhost:38486/logincallback",
		MockAuth:    true,
	}

	// Use test port 38486 to prevent collision
	stop, err := StartLocalCallbackServer(cfg, 38486)
	require.NoError(t, err)
	defer stop()

	// Call /logincallback with mock code
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:38486/logincallback?code=mock-code:tester@gmail.com")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "tester@gmail.com")
	assert.Contains(t, string(body), "Google Authentication Successful")
}

func TestPKCE_GenerateAndWireAuthURL(t *testing.T) {
	pkce, err := GeneratePKCE()
	require.NoError(t, err)
	require.NotEmpty(t, pkce.Verifier)
	require.NotEmpty(t, pkce.Challenge)
	assert.Equal(t, "S256", pkce.Method)

	cfg := &GoogleDesktopConfig{
		ClientID:    "test-client-id",
		RedirectURI: "http://localhost:38485/logincallback",
	}

	authURL := GenerateAuthURL(cfg, "alice@example.com", "nonce-xyz", pkce.Challenge)
	assert.Contains(t, authURL, "code_challenge="+pkce.Challenge)
	assert.Contains(t, authURL, "code_challenge_method=S256")
	assert.Contains(t, authURL, "state=nonce-xyz")
}

func TestStartLocalCallbackServer_CSRFStateValidation(t *testing.T) {
	cfg := &GoogleDesktopConfig{
		ClientID:    "mock-id",
		RedirectURI: "http://localhost:38487/logincallback",
		MockAuth:    true,
	}

	stop, err := StartLocalCallbackServer(cfg, 38487, "expected-nonce-42")
	require.NoError(t, err)
	defer stop()

	client := &http.Client{Timeout: 3 * time.Second}

	// 1. Wrong state should fail CSRF check
	respBad, err := client.Get("http://127.0.0.1:38487/logincallback?code=mock-code:alice@test.com&state=wrong-nonce")
	require.NoError(t, err)
	defer respBad.Body.Close()
	bodyBad, _ := io.ReadAll(respBad.Body)
	assert.Contains(t, string(bodyBad), "CSRF state parameter mismatch")

	// 2. Correct state should succeed
	respGood, err := client.Get("http://127.0.0.1:38487/logincallback?code=mock-code:alice@test.com&state=expected-nonce-42")
	require.NoError(t, err)
	defer respGood.Body.Close()
	bodyGood, _ := io.ReadAll(respGood.Body)
	assert.Contains(t, string(bodyGood), "Google Authentication Successful")
}

func TestVerifyToken_FailsClosedOnEmptyClientIDInProduction(t *testing.T) {
	t.Setenv("DVFS_AUTH_MOCK", "false")

	// In production mode with clientID="", VerifyToken must fail closed
	_, err := VerifyToken(context.Background(), "some-real-looking-token", "user@gmail.com", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GOOGLE_CLIENT_ID")
}


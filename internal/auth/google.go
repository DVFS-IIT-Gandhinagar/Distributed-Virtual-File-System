package auth

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	DefaultRedirectPort = 38485
	DefaultRedirectURI  = "http://localhost:38485/logincallback"
	GoogleAuthEndpoint  = "https://accounts.google.com/o/oauth2/v2/auth"
	GoogleTokenEndpoint = "https://oauth2.googleapis.com/token"
	GoogleInfoEndpoint  = "https://oauth2.googleapis.com/tokeninfo"
)

// GoogleDesktopConfig holds OAuth credentials and endpoints for Desktop client flow.
type GoogleDesktopConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	MockAuth     bool
}

// TokenResponse represents the JSON response from Google's token endpoint.
type TokenResponse struct {
	AccessToken string `json:"access_token"`
	IdToken     string `json:"id_token"`
	ExpiresIn   int    `json:"expires_in"`
	TokenType   string `json:"token_type"`
}

// TokenClaims holds verified user claims extracted from Google ID token.
type TokenClaims struct {
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Audience      string `json:"aud"`
	Expiry        int64  `json:"exp"`
}

type cachedClaim struct {
	claims *TokenClaims
	expiry time.Time
}

var (
	tokenCacheMu sync.RWMutex
	tokenCache   = make(map[string]*cachedClaim)
)

// LoadEnv loads .env files if variables are not already present in the environment.
func LoadEnv(paths ...string) {
	if len(paths) == 0 {
		paths = []string{".env", "../.env", "../../.env"}
	}
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				val := strings.TrimSpace(parts[1])
				val = strings.Trim(val, `"'`)
				if os.Getenv(key) == "" {
					_ = os.Setenv(key, val)
				}
			}
		}
		_ = f.Close()
	}
}

// LoadDesktopConfig loads Google OAuth settings from environment or .env.
func LoadDesktopConfig(envPaths ...string) *GoogleDesktopConfig {
	LoadEnv(envPaths...)

	clientID := strings.TrimSpace(os.Getenv("GOOGLE_CLIENT_ID"))
	clientSecret := strings.TrimSpace(os.Getenv("GOOGLE_CLIENT_SECRET"))
	redirectURI := strings.TrimSpace(os.Getenv("GOOGLE_REDIRECT_URI"))
	if redirectURI == "" {
		redirectURI = DefaultRedirectURI
	}

	mockAuth := strings.EqualFold(os.Getenv("DVFS_AUTH_MOCK"), "true") || strings.EqualFold(os.Getenv("DVFS_AUTH_MOCK"), "1")

	return &GoogleDesktopConfig{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURI:  redirectURI,
		MockAuth:     mockAuth,
	}
}

// GenerateRandomState generates a cryptographically secure random state nonce.
func GenerateRandomState() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// GenerateAuthURL builds the accounts.google.com authorization URL for Desktop OAuth flow.
func GenerateAuthURL(cfg *GoogleDesktopConfig, email string, state string) string {
	if state == "" {
		state = GenerateRandomState()
	}

	params := url.Values{}
	params.Set("client_id", cfg.ClientID)
	params.Set("redirect_uri", cfg.RedirectURI)
	params.Set("response_type", "code")
	params.Set("scope", "openid email profile")
	params.Set("state", state)
	params.Set("prompt", "consent")
	if email != "" {
		params.Set("login_hint", strings.TrimSpace(strings.ToLower(email)))
	}

	return fmt.Sprintf("%s?%s", GoogleAuthEndpoint, params.Encode())
}

// ExchangeCodeForTokens exchanges the OAuth authorization code for tokens at Google's token endpoint.
func ExchangeCodeForTokens(ctx context.Context, cfg *GoogleDesktopConfig, code string, redirectURI string) (*TokenResponse, error) {
	if cfg.MockAuth || strings.HasPrefix(code, "mock-code:") {
		// Mock token generation for test/offline environments
		email := "user@gmail.com"
		if strings.HasPrefix(code, "mock-code:") {
			email = strings.TrimPrefix(code, "mock-code:")
		}
		mockJWT := MockToken(email)
		return &TokenResponse{
			AccessToken: "mock-access-token",
			IdToken:     mockJWT,
			ExpiresIn:   3600,
			TokenType:   "Bearer",
		}, nil
	}

	if redirectURI == "" {
		redirectURI = cfg.RedirectURI
	}

	data := url.Values{}
	data.Set("client_id", cfg.ClientID)
	data.Set("client_secret", cfg.ClientSecret)
	data.Set("code", code)
	data.Set("grant_type", "authorization_code")
	data.Set("redirect_uri", redirectURI)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, GoogleTokenEndpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("google token exchange returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var tokResp TokenResponse
	if err := json.Unmarshal(bodyBytes, &tokResp); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	return &tokResp, nil
}

// MockToken generates a structured mock Google ID token JWT for automated tests.
func MockToken(email string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims := map[string]interface{}{
		"iss":            "https://accounts.google.com",
		"sub":            "mock-sub-12345",
		"email":          strings.TrimSpace(strings.ToLower(email)),
		"email_verified": true,
		"aud":            "mock-client-id",
		"exp":            time.Now().Add(24 * time.Hour).Unix(),
		"iat":            time.Now().Unix(),
	}
	claimsBytes, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(claimsBytes)
	signature := base64.RawURLEncoding.EncodeToString([]byte("mock-signature"))
	return fmt.Sprintf("mock-jwt.%s.%s.%s", header, payload, signature)
}

// ExtractEmailFromToken decodes the payload of a JWT without full cryptographic verification.
func ExtractEmailFromToken(rawToken string) string {
	parts := strings.Split(rawToken, ".")
	if len(parts) >= 2 {
		payloadPart := parts[1]
		if strings.HasPrefix(rawToken, "mock-jwt.") && len(parts) >= 3 {
			payloadPart = parts[2]
		}
		data, err := base64.RawURLEncoding.DecodeString(payloadPart)
		if err == nil {
			var m map[string]interface{}
			if json.Unmarshal(data, &m) == nil {
				if email, ok := m["email"].(string); ok {
					return strings.TrimSpace(strings.ToLower(email))
				}
			}
		}
	}
	return ""
}

// VerifyToken validates an ID token against Google's tokeninfo API or local mock, ensuring email matches.
func VerifyToken(ctx context.Context, rawToken string, expectedEmail string, clientID string) (*TokenClaims, error) {
	rawToken = strings.TrimSpace(rawToken)
	expectedEmail = strings.TrimSpace(strings.ToLower(expectedEmail))
	if rawToken == "" {
		return nil, fmt.Errorf("empty authentication token")
	}

	// 1. Check in-memory cache
	now := time.Now()
	tokenCacheMu.RLock()
	cached, ok := tokenCache[rawToken]
	tokenCacheMu.RUnlock()
	if ok && now.Before(cached.expiry) {
		if expectedEmail != "" && !strings.EqualFold(cached.claims.Email, expectedEmail) {
			return nil, fmt.Errorf("token email '%s' does not match expected email '%s'", cached.claims.Email, expectedEmail)
		}
		return cached.claims, nil
	}

	// 2. Handle Mock Tokens
	isMock := strings.HasPrefix(rawToken, "mock-jwt.") || strings.EqualFold(os.Getenv("DVFS_AUTH_MOCK"), "true") || strings.EqualFold(os.Getenv("DVFS_AUTH_MOCK"), "1")
	if isMock {
		claims, err := parseMockToken(rawToken)
		if err != nil {
			return nil, fmt.Errorf("mock token parse error: %w", err)
		}
		if expectedEmail != "" && !strings.EqualFold(claims.Email, expectedEmail) {
			return nil, fmt.Errorf("mock token email '%s' does not match expected email '%s'", claims.Email, expectedEmail)
		}
		// Cache mock token
		tokenCacheMu.Lock()
		tokenCache[rawToken] = &cachedClaim{
			claims: claims,
			expiry: time.Unix(claims.Expiry, 0),
		}
		tokenCacheMu.Unlock()
		return claims, nil
	}

	// 3. Query Google tokeninfo endpoint
	infoURL := fmt.Sprintf("%s?id_token=%s", GoogleInfoEndpoint, url.QueryEscape(rawToken))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, infoURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create tokeninfo request: %w", err)
	}

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token validation network error: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read tokeninfo response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("invalid google token (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var rawMap map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &rawMap); err != nil {
		return nil, fmt.Errorf("failed to parse tokeninfo json: %w", err)
	}

	email, _ := rawMap["email"].(string)
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
		return nil, fmt.Errorf("tokeninfo did not contain an email address")
	}

	emailVerified := false
	switch v := rawMap["email_verified"].(type) {
	case bool:
		emailVerified = v
	case string:
		emailVerified = strings.EqualFold(v, "true")
	}
	if !emailVerified {
		return nil, fmt.Errorf("google email is not verified for account %s", email)
	}

	var expInt int64
	if expVal, exists := rawMap["exp"]; exists {
		switch v := expVal.(type) {
		case float64:
			expInt = int64(v)
		case string:
			fmt.Sscanf(v, "%d", &expInt)
		}
	}
	if expInt > 0 && now.Unix() > expInt {
		return nil, fmt.Errorf("token has expired (expiry: %d, now: %d)", expInt, now.Unix())
	}

	aud, _ := rawMap["aud"].(string)
	if clientID != "" && aud != "" && aud != clientID {
		log.Printf("[AUTH WARNING] Token audience '%s' differs from configured clientID '%s'", aud, clientID)
	}

	if expectedEmail != "" && !strings.EqualFold(email, expectedEmail) {
		return nil, fmt.Errorf("authenticated email '%s' does not match expected user email '%s'", email, expectedEmail)
	}

	claims := &TokenClaims{
		Email:         email,
		EmailVerified: emailVerified,
		Audience:      aud,
		Expiry:        expInt,
	}

	// Cache verified token
	ttl := 1 * time.Hour
	if expInt > now.Unix() {
		ttl = time.Duration(expInt-now.Unix()) * time.Second
		if ttl > 2*time.Hour {
			ttl = 2 * time.Hour
		}
	}
	tokenCacheMu.Lock()
	tokenCache[rawToken] = &cachedClaim{
		claims: claims,
		expiry: now.Add(ttl),
	}
	tokenCacheMu.Unlock()

	return claims, nil
}

func parseMockToken(rawToken string) (*TokenClaims, error) {
	parts := strings.Split(rawToken, ".")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid token segments")
	}
	payloadSegment := parts[1]
	if strings.HasPrefix(rawToken, "mock-jwt.") && len(parts) >= 3 {
		payloadSegment = parts[2]
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(payloadSegment)
	if err != nil {
		return nil, fmt.Errorf("failed to base64url decode mock payload: %w", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(payloadBytes, &m); err != nil {
		return nil, fmt.Errorf("failed to unmarshal mock claims: %w", err)
	}

	email, _ := m["email"].(string)
	var expInt int64
	if expVal, exists := m["exp"]; exists {
		if f, ok := expVal.(float64); ok {
			expInt = int64(f)
		}
	}
	if expInt == 0 {
		expInt = time.Now().Add(24 * time.Hour).Unix()
	}

	return &TokenClaims{
		Email:         strings.TrimSpace(strings.ToLower(email)),
		EmailVerified: true,
		Audience:      "mock-client-id",
		Expiry:        expInt,
	}, nil
}

// HTML template for the callback page
var callbackPageTemplate = template.Must(template.New("callback").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>DVFS - Google Authentication</title>
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <link rel="preconnect" href="https://fonts.googleapis.com">
  <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
  <link href="https://fonts.googleapis.com/css2?family=Fira+Code:wght@400;500&family=Inter:wght@400;500;600;700&display=swap" rel="stylesheet">
  <style>
    :root {
      --bg-dark: #09090b;
      --card-bg: rgba(24, 24, 27, 0.65);
      --card-border: rgba(255, 255, 255, 0.08);
      --text-main: #f8fafc;
      --text-muted: #a1a1aa;
      --primary: #4f46e5;
      --primary-hover: #4338ca;
      --success: #10b981;
      --success-bg: rgba(16, 185, 129, 0.1);
      --danger: #ef4444;
      --danger-bg: rgba(239, 68, 68, 0.1);
      --input-bg: #000000;
      --input-border: #27272a;
    }

    * { box-sizing: border-box; margin: 0; padding: 0; }

    body {
      background-color: var(--bg-dark);
      background-image: 
        radial-gradient(at 0% 0%, rgba(79, 70, 229, 0.15) 0px, transparent 50%),
        radial-gradient(at 100% 100%, rgba(16, 185, 129, 0.1) 0px, transparent 50%);
      color: var(--text-main);
      font-family: 'Inter', -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
      display: flex;
      align-items: center;
      justify-content: center;
      min-height: 100vh;
      padding: 20px;
    }

    .card {
      background: var(--card-bg);
      backdrop-filter: blur(16px);
      -webkit-backdrop-filter: blur(16px);
      border: 1px solid var(--card-border);
      border-radius: 20px;
      max-width: 480px;
      width: 100%;
      padding: 40px;
      box-shadow: 0 25px 50px -12px rgba(0,0,0,0.5), inset 0 1px 0 rgba(255,255,255,0.05);
      animation: slideUp 0.6s cubic-bezier(0.16, 1, 0.3, 1) forwards;
      opacity: 0;
      transform: translateY(20px);
    }

    @keyframes slideUp {
      to { opacity: 1; transform: translateY(0); }
    }

    .icon-container { display: flex; justify-content: center; margin-bottom: 20px; }
    .icon-container svg { width: 56px; height: 56px; }
    .icon-success { color: var(--success); filter: drop-shadow(0 0 12px rgba(16, 185, 129, 0.4)); }
    .icon-error { color: var(--danger); filter: drop-shadow(0 0 12px rgba(239, 68, 68, 0.4)); }

    h1 { font-size: 24px; font-weight: 700; text-align: center; margin-bottom: 8px; letter-spacing: -0.02em; }
    .subtitle { text-align: center; color: var(--text-muted); font-size: 14px; margin-bottom: 32px; font-weight: 400; }

    .user-badge {
      display: flex;
      align-items: center;
      justify-content: center;
      gap: 8px;
      background: var(--success-bg);
      border: 1px solid rgba(16, 185, 129, 0.2);
      color: #34d399;
      padding: 12px 16px;
      border-radius: 10px;
      font-size: 14px;
      margin-bottom: 28px;
      word-break: break-all;
    }

    .error-box {
      background: var(--danger-bg);
      border: 1px solid rgba(239, 68, 68, 0.2);
      color: #fca5a5;
      padding: 16px;
      border-radius: 10px;
      font-size: 14px;
      margin-bottom: 28px;
      text-align: center;
      line-height: 1.5;
    }

    .label { font-size: 13px; color: var(--text-muted); margin-bottom: 8px; display: block; font-weight: 500; }
    
    .token-wrapper { position: relative; margin-bottom: 24px; }
    textarea {
      width: 100%;
      height: 100px;
      background: var(--input-bg);
      border: 1px solid var(--input-border);
      color: #a78bfa;
      border-radius: 12px;
      padding: 16px;
      font-family: 'Fira Code', monospace;
      font-size: 13px;
      line-height: 1.5;
      resize: none;
      outline: none;
      transition: all 0.2s ease;
      box-shadow: inset 0 2px 4px rgba(0,0,0,0.3);
    }
    textarea:focus { border-color: var(--primary); box-shadow: inset 0 2px 4px rgba(0,0,0,0.3), 0 0 0 3px rgba(79, 70, 229, 0.1); }

    .btn {
      display: flex;
      align-items: center;
      justify-content: center;
      gap: 8px;
      width: 100%;
      background: var(--primary);
      color: #ffffff;
      border: none;
      padding: 14px;
      border-radius: 12px;
      font-size: 15px;
      font-weight: 600;
      cursor: pointer;
      transition: all 0.2s ease;
      box-shadow: 0 4px 12px rgba(79, 70, 229, 0.3);
    }
    .btn svg { width: 18px; height: 18px; }
    .btn:hover { background: var(--primary-hover); transform: translateY(-1px); box-shadow: 0 6px 16px rgba(79, 70, 229, 0.4); }
    .btn:active { transform: translateY(0); box-shadow: 0 2px 8px rgba(79, 70, 229, 0.3); }
    
    .btn.copied { background: var(--success); box-shadow: 0 4px 12px rgba(16, 185, 129, 0.3); }
    .btn.copied:hover { background: #059669; }

    .footer {
      text-align: center;
      font-size: 13px;
      color: var(--text-muted);
      margin-top: 28px;
      line-height: 1.6;
    }
    .highlight {
      color: #e2e8f0;
      font-family: 'Fira Code', monospace;
      font-size: 12px;
      background: rgba(255,255,255,0.1);
      padding: 3px 6px;
      border-radius: 6px;
      border: 1px solid rgba(255,255,255,0.05);
    }
  </style>
</head>
<body>
  <div class="card">
    {{ if .Error }}
      <!-- ERROR STATE -->
      <div class="icon-container">
        <svg class="icon-error" fill="none" stroke="currentColor" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z"></path>
        </svg>
      </div>
      <h1>Authentication Failed</h1>
      <div class="subtitle">An error occurred while completing Google Authentication</div>
      <div class="error-box">{{ .Error }}</div>
      <div class="footer">You may close this tab and try running the <span class="highlight">DVFS</span> client again.</div>
    
    {{ else }}
      <!-- SUCCESS STATE -->
      <div class="icon-container">
        <svg class="icon-success" fill="none" stroke="currentColor" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m5.618-4.016A11.955 11.955 0 0112 2.944a11.955 11.955 0 01-8.618 3.04A12.02 12.02 0 003 9c0 5.591 3.824 10.29 9 11.622 5.176-1.332 9-6.03 9-11.622 0-1.042-.133-2.052-.382-3.016z"></path>
        </svg>
      </div>
      <h1>Authentication Successful</h1>
      <div class="subtitle">Distributed Virtual File System (DVFS)</div>
      
      <div class="user-badge">
        <svg fill="none" stroke="currentColor" viewBox="0 0 24 24" width="16" height="16" xmlns="http://www.w3.org/2000/svg"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M16 7a4 4 0 11-8 0 4 4 0 018 0zM12 14a7 7 0 00-7 7h14a7 7 0 00-7-7z"></path></svg>
        Signed in as <strong>{{ .Email }}</strong>
      </div>
      
      <label class="label" for="tokenBox">Your Authentication Token</label>
      <div class="token-wrapper">
        <textarea id="tokenBox" readonly spellcheck="false">{{ .Token }}</textarea>
      </div>
      
      <button id="copyBtn" class="btn" onclick="copyToken()">
        <svg id="btnIcon" fill="none" stroke="currentColor" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 5H6a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2v-1M8 5a2 2 0 002 2h2a2 2 0 002-2M8 5a2 2 0 012-2h2a2 2 0 012 2m0 0h2a2 2 0 012 2v3m2 4H10m0 0l3-3m-3 3l3 3"></path></svg>
        <span id="btnText">Copy Token to Clipboard</span>
      </button>
      
      <div class="footer">
        Return to your <span class="highlight">DVFS terminal</span> and paste this token to finish logging in.
      </div>
    {{ end }}
  </div>

  <script>
    function copyToken() {
      const box = document.getElementById('tokenBox');
      const btn = document.getElementById('copyBtn');
      const btnText = document.getElementById('btnText');
      const btnIcon = document.getElementById('btnIcon');
      
      box.select();
      box.setSelectionRange(0, 99999);
      
      const originalSVG = '<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 5H6a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2v-1M8 5a2 2 0 002 2h2a2 2 0 002-2M8 5a2 2 0 012-2h2a2 2 0 012 2m0 0h2a2 2 0 012 2v3m2 4H10m0 0l3-3m-3 3l3 3"></path>';
      const successSVG = '<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7"></path>';

      navigator.clipboard.writeText(box.value).then(() => {
        btnText.textContent = 'Copied to Clipboard!';
        btnIcon.innerHTML = successSVG;
        btn.classList.add('copied');
        
        setTimeout(() => {
          btnText.textContent = 'Copy Token to Clipboard';
          btnIcon.innerHTML = originalSVG;
          btn.classList.remove('copied');
        }, 3000);
      }).catch(err => {
        alert('Could not copy automatically. Please select all text in the box and press Ctrl+C / Cmd+C.');
      });
    }
  </script>
</body>
</html>`))

type callbackPageData struct {
	Email string
	Token string
	Error string
}

// RenderCallbackHTML writes the callback web page HTML to the response.
func RenderCallbackHTML(w io.Writer, email, token, errMsg string) error {
	return callbackPageTemplate.Execute(w, callbackPageData{
		Email: email,
		Token: token,
		Error: errMsg,
	})
}

// StartLocalCallbackServer starts a temporary loopback HTTP listener on the given port (e.g. 38485).
func StartLocalCallbackServer(cfg *GoogleDesktopConfig, port int) (stopFunc func(), err error) {
	if port <= 0 {
		port = DefaultRedirectPort
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		// Port may already be bound by a background server or admin console
		return nil, fmt.Errorf("failed to bind loopback callback listener on port %d: %w", port, err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/logincallback", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")

		errParam := r.URL.Query().Get("error")
		if errParam != "" {
			_ = RenderCallbackHTML(w, "", "", fmt.Sprintf("Google returned error: %s", errParam))
			return
		}

		code := r.URL.Query().Get("code")
		if code == "" {
			_ = RenderCallbackHTML(w, "", "", "No authorization code found in callback query parameters.")
			return
		}

		tokResp, err := ExchangeCodeForTokens(r.Context(), cfg, code, cfg.RedirectURI)
		if err != nil {
			_ = RenderCallbackHTML(w, "", "", fmt.Sprintf("Failed to exchange code for token: %v", err))
			return
		}

		token := tokResp.IdToken
		if token == "" {
			token = tokResp.AccessToken
		}

		email := ExtractEmailFromToken(token)
		if email == "" {
			email = "Google User"
		}

		_ = RenderCallbackHTML(w, email, token, "")
	})

	server := &http.Server{
		Handler: mux,
	}

	go func() {
		_ = server.Serve(listener)
	}()

	stop := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		_ = listener.Close()
	}

	return stop, nil
}

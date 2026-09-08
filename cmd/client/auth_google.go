//go:build use_google_auth

package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/auth"
)

// performUserAuth prompts for an email, displays the Google OAuth link, launches the local callback server,
// prompts the user for the token, and verifies it.
func performUserAuth(cliUsername string) (string, string, error) {
	cfg := auth.LoadDesktopConfig()

	if cfg.ClientID == "" && !cfg.MockAuth {
		return "", "", fmt.Errorf("Google OAuth client ID is not configured. " +
			"Please set the GOOGLE_CLIENT_ID environment variable, or supply a .env file containing GOOGLE_CLIENT_ID=<client-id>")
	}

	fmt.Println("=========================================================")
	fmt.Println("       Distributed Virtual File System (DVFS)")
	fmt.Println("            Google Authentication Required")
	fmt.Println("=========================================================")

	reader := bufio.NewReader(os.Stdin)

	// 1. Prompt for email
	var email string
	for {
		fmt.Print("Enter your email: ")
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", "", fmt.Errorf("failed to read email: %w", err)
		}
		email = strings.TrimSpace(strings.ToLower(line))
		if email == "" {
			fmt.Println("Email cannot be empty. Please try again.")
			continue
		}
		if !strings.Contains(email, "@") || !strings.Contains(email, ".") {
			fmt.Println("Invalid email format (must contain '@' and domain). Please try again.")
			continue
		}
		break
	}

	// 2. Generate PKCE & CSRF state
	pkce, _ := auth.GeneratePKCE()
	var codeChallenge, codeVerifier string
	if pkce != nil {
		codeChallenge = pkce.Challenge
		codeVerifier = pkce.Verifier
	}
	state := auth.GenerateRandomState()

	// 3. Start local callback listener on port 38485 with state verification and PKCE verifier
	stopServer, err := auth.StartLocalCallbackServer(cfg, auth.DefaultRedirectPort, state, codeVerifier)
	if err != nil {
		log.Printf("[AUTH NOTICE] Could not bind callback listener on port %d (%v); will rely on existing handler", auth.DefaultRedirectPort, err)
	} else {
		defer stopServer()
	}

	// 4. Generate OAuth link with PKCE S256 challenge
	authURL := auth.GenerateAuthURL(cfg, email, state, codeChallenge)
	fmt.Println("\nPlease open the following URL in your web browser to sign in:")
	fmt.Println("─────────────────────────────────────────────────────────")
	fmt.Println(authURL)
	fmt.Println("─────────────────────────────────────────────────────────")
	fmt.Println("After signing in, your browser will open the DVFS callback page.")
	fmt.Println("Copy the token displayed on that page and paste it below.")

	// 4. Prompt for token
	var token string
	for {
		fmt.Print("\nPaste your token: ")
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", "", fmt.Errorf("failed to read token: %w", err)
		}
		token = strings.TrimSpace(line)
		if token == "" {
			fmt.Println("Token cannot be empty. Please paste the token from the browser page.")
			continue
		}
		break
	}

	// 5. Verify token
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	claims, err := auth.VerifyToken(ctx, token, email, cfg.ClientID)
	if err != nil {
		return "", "", fmt.Errorf("token verification failed: %w", err)
	}

	fmt.Printf("\n✓ Authentication successful! Welcome, %s\n\n", claims.Email)
	return claims.Email, token, nil
}

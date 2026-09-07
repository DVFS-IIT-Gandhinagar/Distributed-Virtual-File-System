//go:build !use_google_auth

package main

// performUserAuth returns the provided username without requiring Google authentication.
func performUserAuth(cliUsername string) (string, string, error) {
	return cliUsername, "", nil
}

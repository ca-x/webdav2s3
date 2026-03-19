// Package auth provides pluggable authentication for the WebDAV server.
// Two modes are supported:
//   - "local": username/password checked against config values
//   - "api":   credentials forwarded to an external HTTP endpoint
package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Authenticator validates credentials.
type Authenticator interface {
	Authenticate(username, password string) (bool, error)
}

// ─────────────────────────────────────────────
// Local authenticator
// ─────────────────────────────────────────────

type localAuth struct {
	username string
	password string
}

// NewLocal creates an authenticator that checks against fixed credentials.
func NewLocal(username, password string) Authenticator {
	return &localAuth{username: username, password: password}
}

func (a *localAuth) Authenticate(username, password string) (bool, error) {
	return username == a.username && password == a.password, nil
}

// ─────────────────────────────────────────────
// API authenticator (mirrors pulsedav design)
// ─────────────────────────────────────────────

type apiAuth struct {
	apiURL string
	client *http.Client
}

type apiRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type apiResponse struct {
	UserID   int    `json:"user_id"`
	Username string `json:"username"`
}

// NewAPI creates an authenticator that delegates to an external HTTP API.
// The API must accept POST JSON {username, password} and return HTTP 200
// with JSON {user_id, username} on success, or non-200 on failure.
func NewAPI(apiURL string) Authenticator {
	return &apiAuth{
		apiURL: apiURL,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (a *apiAuth) Authenticate(username, password string) (bool, error) {
	body, _ := json.Marshal(apiRequest{Username: username, Password: password})
	resp, err := a.client.Post(a.apiURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return false, fmt.Errorf("auth API error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, nil
	}
	var result apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, fmt.Errorf("auth API response decode error: %w", err)
	}
	return result.UserID > 0, nil
}

// Package deviceauth exchanges the device's API key for a one-time browser
// login URL, so front-ends can open the web dashboard already signed in.
package deviceauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/tokitoki-dev/tokitoki-cli/internal/buildinfo"
)

// DashboardURL asks the server to mint a single-use login URL for the user
// who owns apiKey. The key travels in the Authorization header — never in a
// URL — and the returned URL contains only the short-lived token.
func DashboardURL(ctx context.Context, baseURL, apiKey string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/auth/device-login", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("User-Agent", buildinfo.UserAgent())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request dashboard login: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("request dashboard login: server returned %s", resp.Status)
	}

	var decoded struct {
		OK  bool   `json:"ok"`
		URL string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return "", fmt.Errorf("request dashboard login: %w", err)
	}
	if !decoded.OK || decoded.URL == "" {
		return "", fmt.Errorf("request dashboard login: server response carried no URL")
	}
	return decoded.URL, nil
}

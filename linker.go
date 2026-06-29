package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// authHeader returns the Authorization header value for server requests: the
// per-client token once linked, otherwise the bootstrap shared key.
func authHeader() string {
	if t := GetSettings().Token; t != "" {
		return "Bearer " + t
	}
	return "Bearer " + apiKey
}

// bootstrapHeader is the shared-key header used for the /register/* endpoints,
// which a not-yet-linked client must reach before it has a token.
func bootstrapHeader() string {
	return "Bearer " + apiKey
}

func registerBase() string {
	return strings.TrimSuffix(serverURL, "/submit")
}

// StartLinking asks the server for a fresh link code to show the user.
func StartLinking() (string, error) {
	req, err := http.NewRequest(http.MethodPost, registerBase()+"/register/start", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", bootstrapHeader())
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("server returned %d", resp.StatusCode)
	}
	var out struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.Code, nil
}

// PollLinking checks whether a code has been linked yet; on success it persists
// the returned token to settings and reports linked=true.
func PollLinking(code string) (bool, error) {
	if code == "" {
		return false, fmt.Errorf("no code")
	}
	u := registerBase() + "/register/status?code=" + url.QueryEscape(code)
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", bootstrapHeader())
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("server returned %d", resp.StatusCode)
	}
	var out struct {
		Linked bool   `json:"linked"`
		Token  string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false, err
	}
	if out.Linked && out.Token != "" {
		s := GetSettings()
		s.Token = out.Token
		UpdateSettings(s)
		return true, nil
	}
	return false, nil
}

// Unlink revokes this client's token on the server and clears it locally so the
// link flow can be re-run (used by the admin reset).
func Unlink() error {
	if GetSettings().Token != "" {
		req, err := http.NewRequest(http.MethodPost, registerBase()+"/register/unlink", nil)
		if err == nil {
			req.Header.Set("Authorization", authHeader())
			if resp, e := (&http.Client{Timeout: 15 * time.Second}).Do(req); e == nil {
				resp.Body.Close()
			}
		}
	}
	s := GetSettings()
	s.Token = ""
	UpdateSettings(s)
	return nil
}

// IsLinked reports whether this client holds a per-client token.
func IsLinked() bool {
	return GetSettings().Token != ""
}

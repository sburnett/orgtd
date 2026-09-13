package gcal

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"time"

	"github.com/zalando/go-keyring"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	keyringService = "orgtd-gcalsync"
	keyringUser    = "refresh-token"
	calendarScope  = "https://www.googleapis.com/auth/calendar.readonly"

	consentTimeout = 5 * time.Minute
)

// HTTPClient returns an *http.Client that authenticates every request
// with an OAuth2 token scoped to calendar.readonly, using clientID/
// clientSecret — an installed-app OAuth2 client the caller creates
// themselves (Google Cloud Console, "Desktop app" type).
//
// A token cached in the OS keychain from a previous run is reused (and
// silently refreshed as needed for the lifetime of the returned client);
// otherwise this runs the interactive installed-app consent flow — a
// local HTTP server catches the redirect, the system browser is opened
// to Google's consent screen — and caches the resulting refresh token in
// the keychain for next time. onConsentURL, if non-nil, is called with
// the consent URL before the browser is opened, so the caller can also
// print it (useful if the browser can't be opened, e.g. over SSH).
func HTTPClient(ctx context.Context, clientID, clientSecret string, onConsentURL func(url string)) (*http.Client, error) {
	if clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("gcal: oauth client id/secret not configured")
	}

	cfg := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     google.Endpoint,
		Scopes:       []string{calendarScope},
	}

	tok, err := tokenFromKeyring()
	if err != nil {
		tok, err = runConsentFlow(ctx, cfg, onConsentURL)
		if err != nil {
			return nil, err
		}
		if err := saveTokenToKeyring(tok); err != nil {
			return nil, fmt.Errorf("gcal: saving token to keychain: %w", err)
		}
	}

	return oauth2.NewClient(ctx, cfg.TokenSource(ctx, tok)), nil
}

// ForgetToken removes any cached token from the OS keychain, forcing the
// next HTTPClient call to run the interactive consent flow again.
func ForgetToken() error {
	err := keyring.Delete(keyringService, keyringUser)
	if err != nil && err != keyring.ErrNotFound {
		return fmt.Errorf("gcal: removing cached token: %w", err)
	}
	return nil
}

func tokenFromKeyring() (*oauth2.Token, error) {
	data, err := keyring.Get(keyringService, keyringUser)
	if err != nil {
		return nil, err
	}
	var tok oauth2.Token
	if err := json.Unmarshal([]byte(data), &tok); err != nil {
		return nil, err
	}
	return &tok, nil
}

func saveTokenToKeyring(tok *oauth2.Token) error {
	data, err := json.Marshal(tok)
	if err != nil {
		return fmt.Errorf("gcal: encoding token: %w", err)
	}
	return keyring.Set(keyringService, keyringUser, string(data))
}

// runConsentFlow drives one round of the OAuth2 installed-app flow: it
// listens on an ephemeral localhost port, points the browser at Google's
// consent screen with that port as the redirect URI (allowed without
// pre-registration for a loopback address, per Google's own installed-app
// guidance), and waits for the resulting redirect.
func runConsentFlow(ctx context.Context, cfg *oauth2.Config, onConsentURL func(string)) (*oauth2.Token, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("gcal: opening local callback listener: %w", err)
	}
	defer listener.Close()

	cfg.RedirectURL = fmt.Sprintf("http://127.0.0.1:%d/callback", listener.Addr().(*net.TCPAddr).Port)

	state, err := randomState()
	if err != nil {
		return nil, fmt.Errorf("gcal: generating oauth state: %w", err)
	}
	authURL := cfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "consent"))

	if onConsentURL != nil {
		onConsentURL(authURL)
	}
	openBrowser(authURL)

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if got := q.Get("state"); got != state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			errCh <- fmt.Errorf("gcal: oauth callback: state mismatch")
			return
		}
		if errStr := q.Get("error"); errStr != "" {
			fmt.Fprintln(w, "Authorization failed; you may close this tab.")
			errCh <- fmt.Errorf("gcal: oauth consent denied: %s", errStr)
			return
		}
		code := q.Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			errCh <- fmt.Errorf("gcal: oauth callback: no code in request")
			return
		}
		fmt.Fprintln(w, "Authorization complete; you may close this tab.")
		codeCh <- code
	})
	server := &http.Server{Handler: mux}
	go server.Serve(listener)
	defer server.Close()

	select {
	case code := <-codeCh:
		tok, err := cfg.Exchange(ctx, code)
		if err != nil {
			return nil, fmt.Errorf("gcal: exchanging auth code: %w", err)
		}
		return tok, nil
	case err := <-errCh:
		return nil, err
	case <-time.After(consentTimeout):
		return nil, fmt.Errorf("gcal: timed out waiting for oauth consent")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func randomState() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// openBrowser best-effort launches the system browser on url. Failure is
// silent: the caller always has onConsentURL's printed link as a
// fallback (e.g. a headless machine with no browser available).
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

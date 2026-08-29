package yt

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/youtube/v3"
)

const (
	credentialFileName  = "credentials.json"
	credentialFileMatch = "client_secret_*.json"
)

func findCredentials(baseDir string) (string, error) {
	explicit := filepath.Join(baseDir, credentialFileName)
	if _, err := os.Stat(explicit); err == nil {
		return explicit, nil
	}
	matches, err := filepath.Glob(filepath.Join(baseDir, credentialFileMatch))
	if err != nil {
		return "", fmt.Errorf("failed to search for credentials file: %v", err)
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("could not find %s or a client_secret_*.json file in %s", credentialFileName, baseDir)
	}
	return matches[0], nil
}

func newOAuthConfig(credPath string) (*oauth2.Config, error) {
	data, err := os.ReadFile(credPath)
	if err != nil {
		return nil, fmt.Errorf("unable to read credentials file %s: %v", credPath, err)
	}
	config, err := google.ConfigFromJSON(data, youtube.YoutubeReadonlyScope)
	if err != nil {
		return nil, fmt.Errorf("unable to parse credentials file %s: %v", credPath, err)
	}
	return config, nil
}

func getClient(ctx context.Context, config *oauth2.Config, tokenFile string) (*http.Client, error) {
	tok, err := tokenFromFile(tokenFile)
	if err != nil {
		tok, err = getTokenFromWeb(config, tokenFile)
		if err != nil {
			return nil, err
		}
	}
	return config.Client(ctx, tok), nil
}

func tokenFromFile(path string) (*oauth2.Token, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	tok := &oauth2.Token{}
	if err := json.NewDecoder(f).Decode(tok); err != nil {
		return nil, fmt.Errorf("unable to decode cached token: %v", err)
	}
	return tok, nil
}

func saveToken(path string, tok *oauth2.Token) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("unable to cache oauth token: %v", err)
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(tok)
}

func getTokenFromWeb(config *oauth2.Config, tokenFile string) (*oauth2.Token, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("unable to open a local callback port: %v", err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	config.RedirectURL = fmt.Sprintf("http://localhost:%d/", port)

	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Printf("\nTo authorize this app, open this URL in your browser:\n\n  %s\n", authURL)
	openBrowser(authURL)

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			authErr := r.URL.Query().Get("error")
			errCh <- fmt.Errorf("authorization failed: %s", authErr)
			fmt.Fprintln(w, "Authorization failed. You can close this window and retry.")
			return
		}
		codeCh <- code
		fmt.Fprintln(w, "Authorization successful! You can close this window and return to the terminal.")
	})
	server := &http.Server{Handler: mux}

	go func() {
		if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	var code string
	select {
	case code = <-codeCh:
	case err := <-errCh:
		server.Close()
		return nil, err
	case <-time.After(5 * time.Minute):
		server.Close()
		return nil, fmt.Errorf("timed out waiting for authorization response")
	}
	server.Close()

	tok, err := config.Exchange(context.Background(), code)
	if err != nil {
		return nil, fmt.Errorf("unable to exchange authorization code for token: %v", err)
	}
	if err := saveToken(tokenFile, tok); err != nil {
		return nil, err
	}
	fmt.Printf("\nCredentials cached to %s for future runs.\n", tokenFile)
	return tok, nil
}

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
	if err := cmd.Start(); err != nil {
		fmt.Printf("\nCould not open your browser automatically. Please open the URL above manually.\n")
	}
}

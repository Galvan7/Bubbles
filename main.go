package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/youtube/v3"
)

const (
	credentialFileName  = "credentials.json"
	credentialFileMatch = "client_secret_*.json"
	tokenFileName       = "token.json"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("error: %v", err)
	}
}

func run() error {
	credPath, err := findCredentials(".")
	if err != nil {
		return err
	}

	config, err := newOAuthConfig(credPath)
	if err != nil {
		return err
	}

	ctx := context.Background()
	client, err := getClient(ctx, config)
	if err != nil {
		return err
	}

	svc, err := youtube.New(client)
	if err != nil {
		return fmt.Errorf("creating youtube client: %v", err)
	}

	playlists, err := fetchAllPlaylists(ctx, svc)
	if err != nil {
		return err
	}
	if len(playlists) == 0 {
		return fmt.Errorf("no playlists found for the authenticated account")
	}

	printPlaylistMenu(playlists)

	choice, err := promptSelection(len(playlists))
	if err != nil {
		return err
	}
	selected := playlists[choice]

	items, err := fetchPlaylistVideos(ctx, svc, selected.Id)
	if err != nil {
		return err
	}

	printVideos(selected, items)

	return nil
}

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

func getClient(ctx context.Context, config *oauth2.Config) (*http.Client, error) {
	tok, err := tokenFromFile(tokenFileName)
	if err != nil {
		tok, err = getTokenFromWeb(config)
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

func getTokenFromWeb(config *oauth2.Config) (*oauth2.Token, error) {
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
	if err := saveToken(tokenFileName, tok); err != nil {
		return nil, err
	}
	fmt.Printf("\nCredentials cached to %s for future runs.\n", tokenFileName)
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

func fetchAllPlaylists(ctx context.Context, svc *youtube.Service) ([]*youtube.Playlist, error) {
	var playlists []*youtube.Playlist
	pageToken := ""
	for {
		call := svc.Playlists.List([]string{"id", "snippet", "contentDetails"}).
			Mine(true).
			MaxResults(50)
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}
		resp, err := call.Context(ctx).Do()
		if err != nil {
			return nil, fmt.Errorf("unable to retrieve playlists: %v", err)
		}
		playlists = append(playlists, resp.Items...)
		pageToken = resp.NextPageToken
		if pageToken == "" {
			break
		}
	}
	return playlists, nil
}

func printPlaylistMenu(playlists []*youtube.Playlist) {
	fmt.Println("\nYour playlists:")
	for i, p := range playlists {
		fmt.Printf("%3d. %s\n", i+1, p.Snippet.Title)
	}
	fmt.Println()
}

func promptSelection(total int) (int, error) {
	fmt.Print("Enter the number of the playlist you want to view: ")
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return 0, fmt.Errorf("failed to read input: %v", err)
		}
		return 0, fmt.Errorf("no input provided")
	}
	raw := strings.TrimSpace(scanner.Text())
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > total {
		return 0, fmt.Errorf("invalid selection %q: must be a number between 1 and %d", raw, total)
	}
	return n - 1, nil
}

func fetchPlaylistVideos(ctx context.Context, svc *youtube.Service, playlistID string) ([]*youtube.PlaylistItem, error) {
	var items []*youtube.PlaylistItem
	pageToken := ""
	for {
		call := svc.PlaylistItems.List([]string{"snippet", "contentDetails"}).
			PlaylistId(playlistID).
			MaxResults(50)
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}
		resp, err := call.Context(ctx).Do()
		if err != nil {
			return nil, fmt.Errorf("unable to retrieve playlist items: %v", err)
		}
		items = append(items, resp.Items...)
		pageToken = resp.NextPageToken
		if pageToken == "" {
			break
		}
	}
	return items, nil
}

func printVideos(playlist *youtube.Playlist, items []*youtube.PlaylistItem) {
	fmt.Printf("\nVideos in %q:\n", playlist.Snippet.Title)
	if len(items) == 0 {
		fmt.Println("  (no videos)")
		return
	}
	for i, item := range items {
		title := item.Snippet.Title
		videoID := item.Snippet.ResourceId.VideoId
		fmt.Printf("%4d. %s\n      https://www.youtube.com/watch?v=%s\n", i+1, title, videoID)
	}
	fmt.Println()
}
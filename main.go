package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/joho/godotenv"
	"google.golang.org/api/youtube/v3"

	"bubbles/internal/ai"
	"bubbles/internal/yt"
)

const (
	cmdPlaylists  = "playlists"
	cmdCategorize = "categorize"
	cmdSuggest    = "suggest"
)

func main() {
	if _, err := os.Stat(".env"); err == nil {
		if err := godotenv.Load(); err != nil {
			log.Printf("warning: could not load .env: %v", err)
		}
	}
	if err := run(os.Args[1:]); err != nil {
		log.Fatalf("error: %v", err)
	}
}

func run(args []string) error {
	cmd, providerStr, fresh, rest, err := parseArgs(args)
	if err != nil {
		return err
	}
	aiConfig, err := ai.NewConfig(providerStr)
	if err != nil {
		return err
	}

	ctx := context.Background()
	client, err := yt.New(ctx, ".", "token.json")
	if err != nil {
		return err
	}

	playlists, err := client.Playlists(ctx)
	if err != nil {
		return err
	}
	if len(playlists) == 0 {
		return fmt.Errorf("no playlists found for the authenticated account")
	}

	switch cmd {
	case cmdPlaylists:
		return runPlaylists(ctx, client, playlists, rest)
	case cmdCategorize:
		return runCategorize(ctx, client, aiConfig, fresh, playlists, rest)
	case cmdSuggest:
		return runAnalysis(cmd, aiConfig, fresh, playlists, rest)
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func runPlaylists(ctx context.Context, client *yt.Client, playlists []*youtube.Playlist, rest []string) error {
	selected, err := selectPlaylist(playlists, firstArg(rest))
	if err != nil {
		return err
	}

	items, err := client.PlaylistVideos(ctx, selected.Id)
	if err != nil {
		return err
	}

	printVideos(selected, items)
	return nil
}

func runAnalysis(cmd string, cfg *ai.Config, fresh bool, playlists []*youtube.Playlist, rest []string) error {
	if err := cfg.RequireKey(); err != nil {
		return err
	}

	selected, err := selectPlaylist(playlists, firstArg(rest))
	if err != nil {
		return err
	}
	_ = fresh

	fmt.Printf("\nRecommending songs for %q... (coming in Phase 3)\n", selected.Snippet.Title)
	return nil
}

func selectPlaylist(playlists []*youtube.Playlist, arg string) (*youtube.Playlist, error) {
	if arg != "" {
		n, err := strconv.Atoi(strings.TrimSpace(arg))
		if err != nil || n < 1 || n > len(playlists) {
			return nil, fmt.Errorf("invalid selection %q: must be a number between 1 and %d", arg, len(playlists))
		}
		return playlists[n-1], nil
	}

	printPlaylistMenu(playlists)
	idx, err := promptSelection(len(playlists))
	if err != nil {
		return nil, err
	}
	return playlists[idx], nil
}

func firstArg(rest []string) string {
	if len(rest) > 0 {
		return rest[0]
	}
	return ""
}

func parseArgs(args []string) (cmd, provider string, fresh bool, rest []string, err error) {
	cmd = cmdPlaylists
	provider = "gemini"
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == cmdPlaylists || a == cmdCategorize || a == cmdSuggest:
			cmd = a
		case a == "--fresh":
			fresh = true
		case a == "--provider":
			if i+1 >= len(args) {
				return "", "", false, nil, fmt.Errorf("--provider requires a value (gemini or groq)")
			}
			i++
			provider = args[i]
		case strings.HasPrefix(a, "--provider="):
			provider = strings.TrimPrefix(a, "--provider=")
		case strings.HasPrefix(a, "-"):
			return "", "", false, nil, fmt.Errorf("unknown flag: %s", a)
		default:
			rest = append(rest, a)
		}
	}
	return cmd, provider, fresh, rest, nil
}

func printPlaylistMenu(playlists []*youtube.Playlist) {
	fmt.Println("\nYour playlists:")
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	for i, p := range playlists {
		fmt.Fprintf(tw, "%3d.\t%s\n", i+1, p.Snippet.Title)
	}
	tw.Flush()
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

func printVideos(playlist *youtube.Playlist, items []*youtube.PlaylistItem) {
	fmt.Printf("\nVideos in %q:\n", playlist.Snippet.Title)
	if len(items) == 0 {
		fmt.Println("  (no videos)")
		return
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	for i, item := range items {
		title := item.Snippet.Title
		videoID := item.Snippet.ResourceId.VideoId
		fmt.Fprintf(tw, "%4d.\t%s\n\t%s\n", i+1, title, "https://www.youtube.com/watch?v="+videoID)
	}
	tw.Flush()
	fmt.Println()
}

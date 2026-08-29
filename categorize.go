package main

import (
	"context"
	"fmt"
	"os"

	"google.golang.org/api/youtube/v3"

	"bubbles/internal/ai"
	"bubbles/internal/analyze"
	"bubbles/internal/yt"
)

func runCategorize(ctx context.Context, ytClient *yt.Client, cfg *ai.Config, fresh bool, playlists []*youtube.Playlist, rest []string) error {
	if err := cfg.RequireKey(); err != nil {
		return err
	}

	selected, err := selectPlaylist(playlists, firstArg(rest))
	if err != nil {
		return err
	}

	g, err := cfg.NewGemini(ctx)
	if err != nil {
		return err
	}
	defer g.Close()

	fmt.Printf("\nFetching and classifying %q...\n", selected.Snippet.Title)
	songs, err := analyze.Categorize(ctx, g, ytClient, selected, ".", fresh)
	if err != nil {
		return err
	}
	return analyze.PrintReport(songs, os.Stdout)
}

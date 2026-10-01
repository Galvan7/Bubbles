// Command snapshot saves the current state of tracked playlists (title +
// video ID + position) to timestamped JSON files, plus a stable "latest" copy.
// Run it periodically; later use ./cmd/pldiff to find videos that went missing.
//
// Usage:
//
//	go run ./cmd/snapshot
//
// Output (in ./snapshots/):
//
//	<PlaylistName>_<playlistID>_latest.json   current state (overwritten each run)
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"bubbles/internal/yt"
)

// Playlists to track. Add more here any time.
var tracked = []struct{ name, id string }{
	{"Drive", "PLWTzzjcQOUAlKNQsF2mDjASo8tiQ9WRFz"},
	{"Love Songs", "PLCkZslETyLLk"},
}

const snapDir = "snapshots"

type snapItem struct {
	Position    int    `json:"position"`
	VideoID     string `json:"video_id"`
	Title       string `json:"title"`
	Unavailable bool   `json:"unavailable"`
}

type snapshot struct {
	PlaylistName string     `json:"playlist_name"`
	PlaylistID   string     `json:"playlist_id"`
	CapturedAt   string     `json:"captured_at"`
	Count        int        `json:"count"`
	Items        []snapItem `json:"items"`
}

func main() {
	_ = godotenv.Load()
	ctx := context.Background()
	c, err := yt.New(ctx, ".", "token.json")
	if err != nil {
		fmt.Println("auth:", err)
		os.Exit(1)
	}

	if err := os.MkdirAll(snapDir, 0o755); err != nil {
		fmt.Println("mkdir:", err)
		os.Exit(1)
	}

	for _, pl := range tracked {
		items, err := c.PlaylistVideos(ctx, pl.id)
		if err != nil {
			fmt.Printf("[%s] fetch error: %v\n", pl.name, err)
			continue
		}

		snap := snapshot{
			PlaylistName: pl.name,
			PlaylistID:   pl.id,
			CapturedAt:   time.Now().Format(time.RFC3339),
			Count:        len(items),
		}
		for _, it := range items {
			if it.Snippet == nil {
				continue
			}
			vid := ""
			if it.Snippet.ResourceId != nil {
				vid = it.Snippet.ResourceId.VideoId
			}
			title := strings.TrimSpace(it.Snippet.Title)
			lt := strings.ToLower(title)
			unavailable := lt == "deleted video" || lt == "private video" ||
				title == "" || it.Snippet.VideoOwnerChannelId == ""
			snap.Items = append(snap.Items, snapItem{
				Position:    int(it.Snippet.Position) + 1,
				VideoID:     vid,
				Title:       title,
				Unavailable: unavailable,
			})
		}

		base := fmt.Sprintf("%s_%s", sanitize(pl.name), pl.id)
		latest := filepath.Join(snapDir, base+"_latest.json")
		if err := writeJSON(latest, snap); err != nil {
			fmt.Printf("[%s] write latest: %v\n", pl.name, err)
			continue
		}
		fmt.Printf("[%s] saved %d items -> %s\n", pl.name, snap.Count, latest)
	}
}

func sanitize(s string) string {
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.ReplaceAll(s, "/", "-")
	return s
}

func writeJSON(path string, v any) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

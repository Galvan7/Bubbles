// Command export fetches every video from a playlist (default "Drive"),
// de-duplicates by video ID, and writes both a JSON and a CSV file that you
// can edit to mark which videos to include in a new playlist.
//
// Usage:
//
//	go run ./cmd/export                 # exports the "Drive" playlist
//	go run ./cmd/export "My Playlist"   # exports a playlist by title
//
// Output files (in the current directory):
//
//	drive_songs.json  — array of songs, each with a "pick" boolean (default false)
//	drive_songs.csv   — same data; set the "pick" column to y/yes/true/1 to select
package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/joho/godotenv"

	"bubbles/internal/analyze"
	"bubbles/internal/yt"
)

type exportSong struct {
	Position int    `json:"position"`
	VideoID  string `json:"video_id"`
	Title    string `json:"title"`
	Artist   string `json:"artist,omitempty"`
	URL      string `json:"url"`
	Pick     bool   `json:"pick"`
}

const (
	jsonFile = "drive_songs.json"
	csvFile  = "drive_songs.csv"
)

func main() {
	_ = godotenv.Load()

	target := "Drive"
	if len(os.Args) > 1 {
		target = os.Args[1]
	}

	ctx := context.Background()
	client, err := yt.New(ctx, ".", "token.json")
	if err != nil {
		fail("auth: %v", err)
	}

	pls, err := client.Playlists(ctx)
	if err != nil {
		fail("listing playlists: %v", err)
	}

	var id string
	for _, p := range pls {
		if strings.EqualFold(strings.TrimSpace(p.Snippet.Title), strings.TrimSpace(target)) {
			id = p.Id
			break
		}
	}
	if id == "" {
		fail("no playlist named %q found", target)
	}

	// Fetch all pages, de-duplicating by video ID and guarding against a
	// repeating next-page token (the bug that caused songs to repeat).
	seen := make(map[string]bool)
	var songs []exportSong
	token := ""
	seenTokens := make(map[string]bool)
	pos := 0
	for {
		items, next, total, err := client.PlaylistVideosPage(ctx, id, token, 50)
		if err != nil {
			fail("fetching page: %v", err)
		}
		for _, it := range items {
			if it.Snippet == nil || it.Snippet.ResourceId == nil {
				continue
			}
			vid := it.Snippet.ResourceId.VideoId
			if vid == "" || seen[vid] {
				continue // skip blanks and duplicates
			}
			seen[vid] = true
			pos++
			artist, _ := analyze.ParseArtistTitle(it.Snippet.Title)
			songs = append(songs, exportSong{
				Position: pos,
				VideoID:  vid,
				Title:    it.Snippet.Title,
				Artist:   artist,
				URL:      "https://www.youtube.com/watch?v=" + vid,
				Pick:     false,
			})
		}
		if next == "" || seenTokens[next] {
			// End of playlist, or a repeating token — stop to avoid a loop.
			_ = total
			break
		}
		seenTokens[next] = true
		token = next
	}

	if err := writeJSON(songs); err != nil {
		fail("writing json: %v", err)
	}
	if err := writeCSV(songs); err != nil {
		fail("writing csv: %v", err)
	}

	fmt.Printf("Exported %d unique videos from %q.\n", len(songs), target)
	fmt.Printf("  - %s\n  - %s\n", jsonFile, csvFile)
	fmt.Println("\nNext: open one of those files, mark the songs you want, then run:")
	fmt.Println("  go run ./cmd/makeplaylist \"My New Playlist\"")
}

func writeJSON(songs []exportSong) error {
	f, err := os.Create(jsonFile)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(songs)
}

func writeCSV(songs []exportSong) error {
	f, err := os.Create(csvFile)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	if err := w.Write([]string{"pick", "position", "video_id", "title", "artist", "url"}); err != nil {
		return err
	}
	for _, s := range songs {
		pick := ""
		if s.Pick {
			pick = "y"
		}
		if err := w.Write([]string{
			pick,
			fmt.Sprintf("%d", s.Position),
			s.VideoID,
			s.Title,
			s.Artist,
			s.URL,
		}); err != nil {
			return err
		}
	}
	return nil
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}

// Command makeplaylist reads the export file produced by ./cmd/export, takes
// every song you marked as picked, and creates a new YouTube playlist from
// them (in file order).
//
// Usage:
//
//	go run ./cmd/makeplaylist "My New Playlist"
//	go run ./cmd/makeplaylist "My New Playlist" drive_songs.csv
//	go run ./cmd/makeplaylist "My New Playlist" drive_songs.json
//
// If no file is given it prefers drive_songs.csv, then drive_songs.json.
//
// Marking a song as picked:
//   - JSON: set  "pick": true
//   - CSV : set the "pick" column to  y / yes / true / 1
package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/joho/godotenv"

	"bubbles/internal/yt"
)

type song struct {
	VideoID string `json:"video_id"`
	Title   string `json:"title"`
	Pick    bool   `json:"pick"`
}

func main() {
	_ = godotenv.Load()

	if len(os.Args) < 2 || strings.TrimSpace(os.Args[1]) == "" {
		fail("usage: makeplaylist \"New Playlist Name\" [drive_songs.csv|drive_songs.json]")
	}
	name := os.Args[1]

	file := ""
	if len(os.Args) > 2 {
		file = os.Args[2]
	} else {
		for _, cand := range []string{"drive_songs.csv", "drive_songs.json"} {
			if _, err := os.Stat(cand); err == nil {
				file = cand
				break
			}
		}
		if file == "" {
			fail("no export file found; run ./cmd/export first")
		}
	}

	picks, err := loadPicks(file)
	if err != nil {
		fail("reading %s: %v", file, err)
	}
	if len(picks) == 0 {
		fail("no songs are marked as picked in %s (set pick=true / pick column to y)", file)
	}

	fmt.Printf("Read %s: %d song(s) marked to pick.\n", file, len(picks))

	ctx := context.Background()
	client, err := yt.New(ctx, ".", "token.json")
	if err != nil {
		fail("auth: %v", err)
	}

	fmt.Printf("Creating playlist %q...\n", name)
	pl, err := client.CreatePlaylist(ctx, name, "Created by Bubbles from a marked export file.")
	if err != nil {
		fail("creating playlist: %v", err)
	}

	added := 0
	for i, s := range picks {
		if err := client.AddToPlaylist(ctx, pl.Id, s.VideoID); err != nil {
			fmt.Fprintf(os.Stderr, "  ! failed to add %q (%s): %v\n", s.Title, s.VideoID, err)
			continue
		}
		added++
		fmt.Printf("  + (%d/%d) %s\n", i+1, len(picks), s.Title)
	}

	fmt.Printf("\nDone. Added %d/%d songs.\n", added, len(picks))
	fmt.Printf("Playlist: https://www.youtube.com/playlist?list=%s\n", pl.Id)
}

// loadPicks reads either a CSV or JSON export and returns only the picked songs.
func loadPicks(file string) ([]song, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(strings.ToLower(file), ".json") {
		return loadPicksJSON(data)
	}
	return loadPicksCSV(data)
}

func loadPicksJSON(data []byte) ([]song, error) {
	var all []song
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, err
	}
	var picks []song
	for _, s := range all {
		if s.Pick && s.VideoID != "" {
			picks = append(picks, s)
		}
	}
	return picks, nil
}

func loadPicksCSV(data []byte) ([]song, error) {
	r := csv.NewReader(strings.NewReader(string(data)))
	r.FieldsPerRecord = -1 // tolerate ragged rows

	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("reading header: %v", err)
	}
	col := map[string]int{}
	for i, h := range header {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	pickIdx, ok1 := col["pick"]
	vidIdx, ok2 := col["video_id"]
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("CSV must have 'pick' and 'video_id' columns")
	}
	titleIdx, hasTitle := col["title"]

	var picks []song
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if pickIdx >= len(rec) || vidIdx >= len(rec) {
			continue
		}
		if !isPicked(rec[pickIdx]) {
			continue
		}
		s := song{VideoID: strings.TrimSpace(rec[vidIdx]), Pick: true}
		if hasTitle && titleIdx < len(rec) {
			s.Title = rec[titleIdx]
		}
		if s.VideoID != "" {
			picks = append(picks, s)
		}
	}
	return picks, nil
}

func isPicked(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "y", "yes", "true", "1", "x":
		return true
	}
	return false
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}

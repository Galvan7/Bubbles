// Command pldiff compares the CURRENT live state of each tracked playlist against
// the most recent saved snapshot (*_latest.json) and reports:
//   - videos that disappeared entirely (removed or so-deleted they fell out)
//   - videos that are still listed but have gone unavailable (deleted/private),
//     naming them using the title recorded in the snapshot.
//
// Usage:
//
//	go run ./cmd/pldiff
//
// Run ./cmd/snapshot first to create a baseline, then run pldiff any time later.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"

	"bubbles/internal/yt"
)

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

	for _, pl := range tracked {
		base := fmt.Sprintf("%s_%s", sanitize(pl.name), pl.id)
		latestPath := filepath.Join(snapDir, base+"_latest.json")

		old, err := loadSnapshot(latestPath)
		if err != nil {
			fmt.Printf("[%s] no baseline snapshot (%v). Run ./cmd/snapshot first.\n", pl.name, err)
			continue
		}

		items, err := c.PlaylistVideos(ctx, pl.id)
		if err != nil {
			fmt.Printf("[%s] fetch error: %v\n", pl.name, err)
			continue
		}

		// Current video IDs still in the playlist, and which are unavailable now.
		curTitleByID := map[string]string{}
		curUnavail := map[string]bool{}
		for _, it := range items {
			if it.Snippet == nil || it.Snippet.ResourceId == nil {
				continue
			}
			vid := it.Snippet.ResourceId.VideoId
			title := strings.TrimSpace(it.Snippet.Title)
			lt := strings.ToLower(title)
			curTitleByID[vid] = title
			curUnavail[vid] = lt == "deleted video" || lt == "private video" ||
				title == "" || it.Snippet.VideoOwnerChannelId == ""
		}

		fmt.Printf("\n==== %s (%s) ====\n", pl.name, pl.id)
		fmt.Printf("baseline: %d items @ %s   now: %d items\n", old.Count, old.CapturedAt, len(items))

		// 1) Items in baseline but no longer in the playlist at all.
		var vanished []snapItem
		for _, oi := range old.Items {
			if _, still := curTitleByID[oi.VideoID]; !still {
				vanished = append(vanished, oi)
			}
		}
		// 2) Items still present but newly unavailable (were fine in baseline).
		var wentDead []snapItem
		for _, oi := range old.Items {
			if curUnavail[oi.VideoID] && !oi.Unavailable {
				wentDead = append(wentDead, oi)
			}
		}

		if len(vanished) == 0 && len(wentDead) == 0 {
			fmt.Println("No losses since baseline. ✅")
			continue
		}

		if len(wentDead) > 0 {
			fmt.Printf("\n  Went UNAVAILABLE (still listed, now deleted/private) — identified by baseline title:\n")
			for _, s := range wentDead {
				fmt.Printf("    • %q  (id=%s)\n", s.Title, s.VideoID)
			}
		}
		if len(vanished) > 0 {
			fmt.Printf("\n  No longer in the playlist (removed or dropped):\n")
			for _, s := range vanished {
				fmt.Printf("    • %q  (id=%s)\n", s.Title, s.VideoID)
			}
		}
	}

	fmt.Println("\nTip: after reviewing, run ./cmd/snapshot again to update the baseline.")
}

func sanitize(s string) string {
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.ReplaceAll(s, "/", "-")
	return s
}

func loadSnapshot(path string) (*snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

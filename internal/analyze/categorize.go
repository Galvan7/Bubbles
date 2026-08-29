package analyze

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/api/youtube/v3"

	"bubbles/internal/ai"
	"bubbles/internal/yt"
)

const (
	categorizeCachePrefix = "categorize_"
	chunkSize             = 100
)

const categorizeInstruction = `You are a music librarian. Classify each track below into exactly one category: Party, Love, Workout, Chill, Sad, Other.
- Party: upbeat, celebratory tracks made for parties and dancing.
- Love: romantic or love songs.
- Workout: high-energy tracks for the gym, running, or sports.
- Chill: relaxed, mellow, lo-fi, ambient, or background-friendly tracks.
- Sad: emotional, melancholic, or breakup tracks.
- Other: anything that does not fit the categories above, including instrumental background music.

Respond ONLY with a single JSON array, one object per numbered track, in the same order. Each object must have exactly these fields:
{"index": <int, the track number>, "category": "<one of Party, Love, Workout, Chill, Sad, Other>", "confidence": <float 0.0 to 1.0>}
Output nothing outside the JSON array.`

const correctiveCategoryInstruction = `Your previous reply was invalid: it was not a proper JSON array, had the wrong number of entries, or used unknown categories. Reply again with ONLY a valid JSON array as specified, one entry per track, correctly indexed.`

type rawResult struct {
	Index      int     `json:"index"`
	Category   string  `json:"category"`
	Confidence float64 `json:"confidence"`
}

type cachedCategorization struct {
	PlaylistID string    `json:"playlist_id"`
	Generated  time.Time `json:"generated"`
	Songs      []Song    `json:"songs"`
}

func Categorize(ctx context.Context, g *ai.Gemini, ytClient *yt.Client, playlist *youtube.Playlist, workDir string, fresh bool) ([]Song, error) {
	cachePath := filepath.Join(workDir, categorizeCachePrefix+playlist.Id+".json")
	if !fresh {
		if cached, err := loadCache(cachePath, playlist.Id); err == nil {
			fmt.Printf("Using cached analysis from %s (use --fresh to reclassify).\n", cached.Generated.Format("2006-01-02 15:04"))
			return cached.Songs, nil
		}
	}

	items, err := ytClient.PlaylistVideos(ctx, playlist.Id)
	if err != nil {
		return nil, err
	}
	fmt.Printf("Fetched %d items\n", len(items))

	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = itemVideoID(it)
	}
	videos, err := ytClient.Videos(ctx, ids)
	if err != nil {
		return nil, err
	}

	songs := make([]Song, 0, len(items))
	for _, it := range items {
		videoID := itemVideoID(it)
		s := Song{
			VideoID: videoID,
			Title:   it.Snippet.Title,
			URL:     "https://www.youtube.com/watch?v=" + videoID,
		}
		if _, ok := videos[videoID]; !ok {
			s.NonMusic = true
		} else {
			s.Title = videos[videoID].Snippet.Title
		}
		artist, title := ParseArtistTitle(s.Title)
		s.Artist = artist
		s.Title = CleanTitle(title)
		songs = append(songs, s)
	}

	var toClassify []*Song
	for i := range songs {
		if songs[i].NonMusic {
			songs[i].Category = "Other"
			songs[i].Confidence = 1
		} else {
			toClassify = append(toClassify, &songs[i])
		}
	}

	chunks := chunkSongs(toClassify, chunkSize)
	for ci, chunk := range chunks {
		instruction := categorizeInstruction
		content := formatSongs(chunk)
		for attempt := 0; ; attempt++ {
			out, err := g.GenerateJSON(ctx, instruction, content)
			if err == nil {
				var results map[int]rawResult
				results, err = parseCategoryResults(out, len(chunk), Categories)
				if err == nil {
					applyResults(chunk, results)
					break
				}
			}
			if attempt >= 1 {
				return nil, fmt.Errorf("classification failed for chunk %d/%d: %v", ci+1, len(chunks), err)
			}
			instruction = correctiveCategoryInstruction
		}
		fmt.Printf("Classified chunk %d/%d (%d tracks)\n", ci+1, len(chunks), len(chunk))
	}

	if err := saveCache(cachePath, playlist, songs); err != nil {
		return nil, err
	}
	return songs, nil
}

func applyResults(chunk []*Song, results map[int]rawResult) {
	for pos, s := range chunk {
		if r, ok := results[pos+1]; ok {
			s.Category = r.Category
			s.Confidence = r.Confidence
		} else {
			s.Category = "Other"
			s.Confidence = 0
		}
	}
}

func parseCategoryResults(out string, n int, allowed []string) (map[int]rawResult, error) {
	out = stripJSONFence(out)
	var raw []rawResult
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("classifier returned invalid JSON: %v", err)
	}
	if len(raw) != n {
		return nil, fmt.Errorf("classifier returned %d entries, expected %d", len(raw), n)
	}
	res := make(map[int]rawResult, len(raw))
	for _, r := range raw {
		if r.Index < 1 || r.Index > n {
			return nil, fmt.Errorf("classifier returned out-of-range index %d", r.Index)
		}
		cat, ok := canonicalCategory(r.Category)
		if !ok {
			return nil, fmt.Errorf("classifier returned unknown category %q", r.Category)
		}
		if r.Confidence < 0 {
			r.Confidence = 0
		}
		if r.Confidence > 1 {
			r.Confidence = 1
		}
		r.Category = cat
		res[r.Index] = r
	}
	return res, nil
}

func stripJSONFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if idx := strings.Index(s, "\n"); idx >= 0 {
			s = s[idx+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	return strings.TrimSpace(s)
}

func formatSongs(chunk []*Song) string {
	var sb strings.Builder
	for i, s := range chunk {
		label := s.Title
		if s.Artist != "" {
			label = s.Artist + " - " + s.Title
		}
		fmt.Fprintf(&sb, "%d. %s\n", i+1, label)
	}
	return strings.TrimSuffix(sb.String(), "\n")
}

func chunkSongs(songs []*Song, size int) [][]*Song {
	if len(songs) == 0 {
		return nil
	}
	var chunks [][]*Song
	for start := 0; start < len(songs); start += size {
		end := start + size
		if end > len(songs) {
			end = len(songs)
		}
		chunks = append(chunks, songs[start:end])
	}
	return chunks
}

func loadCache(path, playlistID string) (cachedCategorization, error) {
	var c cachedCategorization
	data, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return c, err
	}
	if c.PlaylistID != playlistID {
		return c, fmt.Errorf("cache belongs to another playlist")
	}
	return c, nil
}

func saveCache(path string, playlist *youtube.Playlist, songs []Song) error {
	c := cachedCategorization{PlaylistID: playlist.Id, Generated: time.Now(), Songs: songs}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func itemVideoID(it *youtube.PlaylistItem) string {
	if it.Snippet != nil && it.Snippet.ResourceId != nil {
		return it.Snippet.ResourceId.VideoId
	}
	return ""
}

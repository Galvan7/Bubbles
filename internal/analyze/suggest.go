package analyze

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"bubbles/internal/ai"
)

const defaultSuggestCount = 20

const maxSamplesPerCategory = 40

type Recommendation struct {
	Title    string `json:"title"`
	Artist   string `json:"artist"`
	Category string `json:"category"`
	Reason   string `json:"reason"`
}

type suggestions struct {
	Recommendations []Recommendation `json:"recommendations"`
}

// Suggest asks Gemini to recommend songs the user is missing, based on the
// categorized contents of their playlist.
func Suggest(ctx context.Context, g *ai.Gemini, songs []Song) ([]Recommendation, error) {
	instruction := fmt.Sprintf(`Act as a music expert and a YouTube playlist curator.
Based on the user's playlist analysis below, recommend %d songs the user would love but does NOT already have.
Return strict JSON matching: {"recommendations":[{"title":"...","artist":"...","category":"...","reason":"why it fits, one short sentence"}]}

Rules:
- category must be exactly one of: %s
- Weight more recommendations toward the user's most-played categories.
- Prefer well-known, popular songs in those genres; avoid obscure tracks.
- Include at least one recommendation for each category the user plays, unless impossible.
- Never suggest a title already present in the sample titles.`, defaultSuggestCount, strings.Join(Categories, ", "))

	raw, err := g.GenerateJSON(ctx, instruction, buildProfile(songs))
	if err != nil {
		return nil, err
	}

	var resp suggestions
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return nil, fmt.Errorf("parsing recommendations: %v", err)
	}
	return normalizeRecommendations(resp.Recommendations), nil
}

func buildProfile(songs []Song) string {
	counts := make(map[string]int)
	byCat := make(map[string][]string)
	seen := make(map[string]bool)

	for _, s := range songs {
		cat := s.Category
		if _, ok := canonicalCategory(cat); !ok {
			cat = "Other"
		}
		counts[cat]++
		if !seen[s.Title] && len(byCat[cat]) < maxSamplesPerCategory {
			seen[s.Title] = true
			byCat[cat] = append(byCat[cat], s.Title)
		}
	}

	var sb strings.Builder
	sb.WriteString("User's playlist analysis:\n")
	for _, cat := range Categories {
		fmt.Fprintf(&sb, "%s: %d songs\n", cat, counts[cat])
	}
	sb.WriteString("\nSample titles per category (up to 40 each):\n")
	for _, cat := range Categories {
		if len(byCat[cat]) == 0 {
			continue
		}
		fmt.Fprintf(&sb, "\n%s: %s\n", cat, strings.Join(byCat[cat], "; "))
	}
	return sb.String()
}

func normalizeRecommendations(recs []Recommendation) []Recommendation {
	out := make([]Recommendation, 0, len(recs))
	seen := make(map[string]bool)
	for _, r := range recs {
		title := strings.TrimSpace(r.Title)
		if title == "" {
			continue
		}
		key := strings.ToLower(title)
		if seen[key] {
			continue
		}
		seen[key] = true
		cat := r.Category
		if c, ok := canonicalCategory(cat); ok {
			cat = c
		} else {
			cat = "Other"
		}
		out = append(out, Recommendation{
			Title:    title,
			Artist:   strings.TrimSpace(r.Artist),
			Category: cat,
			Reason:   strings.TrimSpace(r.Reason),
		})
	}
	if len(out) > defaultSuggestCount {
		out = out[:defaultSuggestCount]
	}
	return out
}

func PrintRecommendations(recs []Recommendation, w io.Writer) error {
	if len(recs) == 0 {
		fmt.Fprintln(w, "No recommendations returned.")
		return nil
	}
	fmt.Fprintf(w, "\nSuggested songs (%d):\n", len(recs))

	byCat := make(map[string][]Recommendation)
	for _, r := range recs {
		byCat[r.Category] = append(byCat[r.Category], r)
	}

	for _, cat := range Categories {
		group := byCat[cat]
		if len(group) == 0 {
			continue
		}
		fmt.Fprintf(w, "\n%s (%d)\n", cat, len(group))
		for i, r := range group {
			name := r.Title
			if r.Artist != "" {
				name += " - " + r.Artist
			}
			fmt.Fprintf(w, "%4d.  %s\n", i+1, name)
			if r.Reason != "" {
				fmt.Fprintf(w, "       %s\n", r.Reason)
			}
		}
	}
	return nil
}

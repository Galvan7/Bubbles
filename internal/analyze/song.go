package analyze

import "strings"

type Song struct {
	VideoID    string  `json:"video_id"`
	Title      string  `json:"title"`
	Artist     string  `json:"artist,omitempty"`
	URL        string  `json:"url"`
	Category   string  `json:"category"`
	Confidence float64 `json:"confidence"`
	NonMusic   bool    `json:"non_music,omitempty"`
}

var Categories = []string{"Party", "Love", "Workout", "Chill", "Sad", "Other"}

func canonicalCategory(c string) (string, bool) {
	for _, cat := range Categories {
		if strings.EqualFold(c, cat) {
			return cat, true
		}
	}
	return "", false
}

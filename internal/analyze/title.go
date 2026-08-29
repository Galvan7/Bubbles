package analyze

import (
	"regexp"
	"strings"
)

var artistSeparators = []string{" - ", " – ", " — ", " | "}

var artistBlocklist = []string{
	"official", "video", "music", "lyric", "lyrical", "audio", "full", "song",
	"hd", "hq", "remix", "mix", "slowed", "sped", "reverb", "visualizer", "mv",
	"teaser", "trailer", "trap", "karaoke", "instrumental", "live", "concert",
	"with lyrics", "bass boosted", "slow", "speed", "lofi",
}

var bracketTag = regexp.MustCompile(`\[[^\]]*\]|\([^)]*\)`)

func ParseArtistTitle(t string) (artist, title string) {
	clean := strings.TrimSpace(t)
	for _, sep := range artistSeparators {
		idx := strings.Index(clean, sep)
		if idx <= 0 {
			continue
		}
		maybeArtist := strings.TrimSpace(clean[:idx])
		rest := strings.TrimSpace(clean[idx+len(sep):])
		if looksLikeArtist(maybeArtist) && rest != "" {
			return maybeArtist, rest
		}
	}
	return "", clean
}

func looksLikeArtist(s string) bool {
	lower := strings.ToLower(s)
	for _, block := range artistBlocklist {
		if strings.Contains(lower, block) {
			return false
		}
	}
	return len(strings.Fields(lower)) <= 4
}

func CleanTitle(t string) string {
	cleaned := bracketTag.ReplaceAllString(t, "")
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	return strings.TrimSpace(cleaned)
}

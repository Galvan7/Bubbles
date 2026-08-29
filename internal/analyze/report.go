package analyze

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

func PrintReport(songs []Song, w io.Writer) error {
	fmt.Fprintf(w, "\nClassification: %d tracks", len(songs))
	for _, cat := range Categories {
		count := 0
		for _, s := range songs {
			if strings.EqualFold(s.Category, cat) {
				count++
			}
		}
		fmt.Fprintf(w, "  |  %s: %d", cat, count)
	}
	fmt.Fprintln(w)

	for _, cat := range Categories {
		var list []Song
		for _, s := range songs {
			if strings.EqualFold(s.Category, cat) {
				list = append(list, s)
			}
		}
		if len(list) == 0 {
			continue
		}

		fmt.Fprintf(w, "\n%s (%d)\n", cat, len(list))
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		for i, s := range list {
			label := displayTitle(s)
			if s.NonMusic {
				label += " (unavailable)"
			}
			fmt.Fprintf(tw, "%4d.\t%s\n\t%s\n", i+1, label, s.URL)
		}
		tw.Flush()
	}
	fmt.Fprintln(w)
	return nil
}

func displayTitle(s Song) string {
	if s.Artist != "" {
		return s.Artist + " – " + s.Title
	}
	return s.Title
}

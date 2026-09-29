package tui

import (
	"testing"

	"github.com/charmbracelet/bubbles/list"
)

func TestChosenInOrder(t *testing.T) {
	items := []list.Item{
		item{title: "A", videoID: "a"},
		item{title: "B", videoID: "b"},
		item{title: "C", videoID: "c"},
		item{title: "D", videoID: "d"},
	}
	checked := map[string]bool{"c": true, "a": true} // pick out of order

	got := chosenInOrder(items, checked)
	if len(got) != 2 {
		t.Fatalf("expected 2 chosen, got %d", len(got))
	}
	// Must preserve original list order: A before C.
	if got[0].videoID != "a" || got[1].videoID != "c" {
		t.Fatalf("expected order [a, c], got [%s, %s]", got[0].videoID, got[1].videoID)
	}
}

func TestChosenInOrderEmpty(t *testing.T) {
	items := []list.Item{item{title: "A", videoID: "a"}}
	if got := chosenInOrder(items, map[string]bool{}); len(got) != 0 {
		t.Fatalf("expected 0 chosen, got %d", len(got))
	}
	// A false value should not count as chosen.
	if got := chosenInOrder(items, map[string]bool{"a": false}); len(got) != 0 {
		t.Fatalf("expected 0 chosen for false value, got %d", len(got))
	}
}

func TestCheckDelegateCount(t *testing.T) {
	d := newCheckDelegate()
	d.checked["x"] = true
	d.checked["y"] = true
	d.checked["z"] = false
	if n := d.count(); n != 2 {
		t.Fatalf("expected count 2, got %d", n)
	}
}

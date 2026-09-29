package main

import "testing"

func TestLoadPicksCSV(t *testing.T) {
	data := []byte("pick,position,video_id,title,artist,url\n" +
		"y,1,aaa,Song A,,http://x\n" +
		",2,bbb,Song B,,http://x\n" +
		"YES,3,ccc,\"Song, C\",,http://x\n" +
		"1,4,ddd,Song D,,http://x\n" +
		"n,5,eee,Song E,,http://x\n")

	picks, err := loadPicksCSV(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(picks) != 3 {
		t.Fatalf("expected 3 picks, got %d", len(picks))
	}
	want := []string{"aaa", "ccc", "ddd"}
	for i, w := range want {
		if picks[i].VideoID != w {
			t.Fatalf("pick %d: want %s, got %s", i, w, picks[i].VideoID)
		}
	}
	if picks[1].Title != "Song, C" {
		t.Fatalf("expected quoted title 'Song, C', got %q", picks[1].Title)
	}
}

func TestLoadPicksJSON(t *testing.T) {
	data := []byte(`[
      {"video_id":"aaa","title":"A","pick":true},
      {"video_id":"bbb","title":"B","pick":false},
      {"video_id":"ccc","title":"C","pick":true},
      {"video_id":"","title":"blank","pick":true}
    ]`)
	picks, err := loadPicksJSON(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(picks) != 2 {
		t.Fatalf("expected 2 picks, got %d", len(picks))
	}
	if picks[0].VideoID != "aaa" || picks[1].VideoID != "ccc" {
		t.Fatalf("unexpected picks: %+v", picks)
	}
}

func TestIsPicked(t *testing.T) {
	for _, s := range []string{"y", "Y", "yes", "TRUE", "1", "x", " y "} {
		if !isPicked(s) {
			t.Fatalf("expected %q to be picked", s)
		}
	}
	for _, s := range []string{"", "n", "no", "0", "false"} {
		if isPicked(s) {
			t.Fatalf("expected %q to NOT be picked", s)
		}
	}
}

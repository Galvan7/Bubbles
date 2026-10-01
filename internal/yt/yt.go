package yt

import (
	"context"
	"fmt"

	"google.golang.org/api/youtube/v3"
)

const tokenFileName = "token.json"

type Client struct {
	svc *youtube.Service
}

func New(ctx context.Context, baseDir, tokenFile string) (*Client, error) {
	credPath, err := findCredentials(baseDir)
	if err != nil {
		return nil, err
	}

	config, err := newOAuthConfig(credPath)
	if err != nil {
		return nil, err
	}

	httpClient, err := getClient(ctx, config, tokenFile)
	if err != nil {
		return nil, err
	}

	svc, err := youtube.New(httpClient)
	if err != nil {
		return nil, fmt.Errorf("creating youtube client: %v", err)
	}
	return &Client{svc: svc}, nil
}

func (c *Client) DefaultTokenFile() string {
	return tokenFileName
}

func (c *Client) Playlists(ctx context.Context) ([]*youtube.Playlist, error) {
	var playlists []*youtube.Playlist
	pageToken := ""
	for {
		call := c.svc.Playlists.List([]string{"id", "snippet", "contentDetails"}).
			Mine(true).
			MaxResults(50)
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}
		resp, err := call.Context(ctx).Do()
		if err != nil {
			return nil, fmt.Errorf("unable to retrieve playlists: %v", err)
		}
		playlists = append(playlists, resp.Items...)
		pageToken = resp.NextPageToken
		if pageToken == "" {
			break
		}
	}
	return playlists, nil
}

func (c *Client) PlaylistVideos(ctx context.Context, playlistID string) ([]*youtube.PlaylistItem, error) {
	var items []*youtube.PlaylistItem
	pageToken := ""
	for {
		call := c.svc.PlaylistItems.List([]string{"snippet", "contentDetails"}).
			PlaylistId(playlistID).
			MaxResults(50)
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}
		resp, err := call.Context(ctx).Do()
		if err != nil {
			return nil, fmt.Errorf("unable to retrieve playlist items: %v", err)
		}
		items = append(items, resp.Items...)
		pageToken = resp.NextPageToken
		if pageToken == "" {
			break
		}
	}
	return items, nil
}

// PlaylistVideosPage fetches a single page (up to pageSize, max 50) of a
// playlist's videos. It returns the items, the token for the next page ("" if
// none), and the total number of videos in the playlist.
func (c *Client) PlaylistVideosPage(ctx context.Context, playlistID, pageToken string, pageSize int64) (items []*youtube.PlaylistItem, nextPageToken string, total int64, err error) {
	if pageSize <= 0 || pageSize > 50 {
		pageSize = 50
	}
	call := c.svc.PlaylistItems.List([]string{"snippet", "contentDetails"}).
		PlaylistId(playlistID).
		MaxResults(pageSize)
	if pageToken != "" {
		call = call.PageToken(pageToken)
	}
	resp, err := call.Context(ctx).Do()
	if err != nil {
		return nil, "", 0, fmt.Errorf("unable to retrieve playlist items: %v", err)
	}
	total = 0
	if resp.PageInfo != nil {
		total = resp.PageInfo.TotalResults
	}
	return resp.Items, resp.NextPageToken, total, nil
}

// CreatePlaylist creates a new (private) playlist and returns it.
func (c *Client) CreatePlaylist(ctx context.Context, title, description string) (*youtube.Playlist, error) {
	pl := &youtube.Playlist{
		Snippet: &youtube.PlaylistSnippet{
			Title:       title,
			Description: description,
		},
		Status: &youtube.PlaylistStatus{
			PrivacyStatus: "private",
		},
	}
	created, err := c.svc.Playlists.Insert([]string{"snippet", "status"}, pl).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("unable to create playlist: %v", err)
	}
	return created, nil
}

// AddToPlaylist appends a single video to a playlist by video ID.
func (c *Client) AddToPlaylist(ctx context.Context, playlistID, videoID string) error {
	item := &youtube.PlaylistItem{
		Snippet: &youtube.PlaylistItemSnippet{
			PlaylistId: playlistID,
			ResourceId: &youtube.ResourceId{
				Kind:    "youtube#video",
				VideoId: videoID,
			},
		},
	}
	_, err := c.svc.PlaylistItems.Insert([]string{"snippet"}, item).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("unable to add video %s to playlist: %v", videoID, err)
	}
	return nil
}

// RemoveFromPlaylist deletes a playlist item by its playlist-item ID (not the
// video ID). Get the ID from PlaylistItem.Id.
func (c *Client) RemoveFromPlaylist(ctx context.Context, playlistItemID string) error {
	if err := c.svc.PlaylistItems.Delete(playlistItemID).Context(ctx).Do(); err != nil {
		return fmt.Errorf("unable to remove playlist item %s: %v", playlistItemID, err)
	}
	return nil
}

// SearchResult is a trimmed video search hit.
type SearchResult struct {
	VideoID string
	Title   string
	Channel string
}

// Search returns up to maxResults video results for the query. Note:
// search.list costs 100 quota units per call.
func (c *Client) Search(ctx context.Context, query string, maxResults int64) ([]SearchResult, error) {
	if maxResults <= 0 || maxResults > 25 {
		maxResults = 5
	}
	resp, err := c.svc.Search.List([]string{"snippet"}).
		Q(query).
		Type("video").
		MaxResults(maxResults).
		Context(ctx).
		Do()
	if err != nil {
		return nil, fmt.Errorf("search failed for %q: %v", query, err)
	}
	var out []SearchResult
	for _, it := range resp.Items {
		if it.Id == nil || it.Id.VideoId == "" || it.Snippet == nil {
			continue
		}
		out = append(out, SearchResult{
			VideoID: it.Id.VideoId,
			Title:   it.Snippet.Title,
			Channel: it.Snippet.ChannelTitle,
		})
	}
	return out, nil
}

func (c *Client) Videos(ctx context.Context, ids []string) (map[string]*youtube.Video, error) {
	byID := make(map[string]*youtube.Video)
	const batch = 50
	for start := 0; start < len(ids); start += batch {
		end := start + batch
		if end > len(ids) {
			end = len(ids)
		}
		resp, err := c.svc.Videos.List([]string{"snippet", "contentDetails"}).
			Id(ids[start:end]...).
			Context(ctx).
			Do()
		if err != nil {
			return nil, fmt.Errorf("unable to retrieve video details: %v", err)
		}
		for _, v := range resp.Items {
			byID[v.Id] = v
		}
	}
	return byID, nil
}

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

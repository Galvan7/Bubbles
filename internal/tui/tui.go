package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"google.golang.org/api/youtube/v3"

	"bubbles/internal/ai"
	"bubbles/internal/analyze"
	"bubbles/internal/yt"
)

type mode int

const (
	modeBoot mode = iota
	modePlaylists
	modeLoadingVideos
	modeVideos
	modeActions
	modeCategorizing
	modeReport
)

const (
	actVideos     = "videos"
	actCategorize = "categorize"
	actReCat      = "recategorize"
	actSuggest    = "suggest"
)

type item struct {
	title string
	desc  string
	value string
}

func (i item) Title() string       { return i.title }
func (i item) Description() string { return i.desc }
func (i item) FilterValue() string { return i.title }

type app struct {
	client    *yt.Client
	aiCfg     *ai.Config
	playlists []*youtube.Playlist
	selected  *youtube.Playlist

	mode       mode
	spinner    spinner.Model
	list       list.Model
	viewport   viewport.Model
	progress   *strings.Builder
	report     string
	fresh      bool
	err        error
	status     string
	progressCh chan string

	width  int
	height int
}

func Run() error {
	ctx := context.Background()
	client, err := yt.New(ctx, ".", "token.json")
	if err != nil {
		return err
	}
	cfg, err := ai.NewConfig("gemini")
	if err != nil {
		return err
	}
	p := tea.NewProgram(newApp(client, cfg), tea.WithAltScreen())
	_, err = p.Run()
	return err
}

func newApp(client *yt.Client, cfg *ai.Config) *app {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	a := &app{
		client:   client,
		aiCfg:    cfg,
		mode:     modeBoot,
		spinner:  s,
		progress: &strings.Builder{},
	}

	dlg := list.NewDefaultDelegate()
	a.list = list.New(nil, dlg, 0, 0)
	a.list.Title = "Your Playlists"
	return a
}

func (a *app) Init() tea.Cmd {
	return tea.Batch(a.spinner.Tick, loadPlaylistsCmd(a.client))
}

func (a *app) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		a.list.SetSize(a.width, a.height-3)
		a.viewport.Width = a.width
		a.viewport.Height = a.height - 4
		a.viewport.SetContent(a.viewport.View())
		return a, nil
	case tea.KeyMsg:
		return a.updateKey(msg)
	case spinner.TickMsg:
		var cmd tea.Cmd
		a.spinner, cmd = a.spinner.Update(msg)
		return a, cmd
	case playlistsLoadedMsg:
		return a.onPlaylistsLoaded(msg)
	case videosLoadedMsg:
		return a.onVideosLoaded(msg)
	case categoryProgressMsg:
		a.progress.WriteString(msg.line + "\n")
		a.viewport.SetContent(a.progress.String())
		a.viewport.GotoBottom()
		return a, waitCategoryProgress(a.progressCh)
	case categoryDoneMsg:
		return a.onCategoryDone(msg)
	}
	return a, nil
}

func (a *app) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return a, tea.Quit
	}

	switch a.mode {
	case modePlaylists, modeActions, modeVideos:
		switch msg.String() {
		case "enter":
			return a.selectCurrent()
		case "esc":
			switch a.mode {
			case modeActions:
				a.mode = modePlaylists
				a.rebuildList(playlistItems(a.playlists), "Your Playlists")
			case modeVideos:
				a.mode = modeActions
				a.rebuildList(actionItems(), fmt.Sprintf("Actions — %s", a.selectedTitle()))
			}
			return a, nil
		}
		var cmd tea.Cmd
		a.list, cmd = a.list.Update(msg)
		return a, cmd

	case modeReport:
		switch msg.String() {
		case "esc", "enter":
			a.mode = modeActions
			a.rebuildList(actionItems(), fmt.Sprintf("Actions — %s", a.selectedTitle()))
			return a, nil
		}
		var cmd tea.Cmd
		a.viewport, cmd = a.viewport.Update(msg)
		return a, cmd

	case modeCategorizing:
		if msg.String() == "esc" {
			a.status = "Classification still running in the background. Esc again once it finishes."
		}
	}

	return a, nil
}

func (a *app) selectCurrent() (tea.Model, tea.Cmd) {
	i, ok := a.list.SelectedItem().(item)
	if !ok {
		return a, nil
	}
	switch a.mode {
	case modePlaylists:
		for _, p := range a.playlists {
			if p.Id == i.value {
				a.selected = p
				break
			}
		}
		a.mode = modeActions
		a.rebuildList(actionItems(), fmt.Sprintf("Actions — %s", a.selectedTitle()))
		return a, nil
	case modeActions:
		switch i.value {
		case actVideos:
			a.mode = modeLoadingVideos
			return a, loadVideosCmd(a.client, a.selected.Id)
		case actCategorize:
			return a.startCategorizing(false)
		case actReCat:
			return a.startCategorizing(true)
		case actSuggest:
			a.status = "Song suggestions arrive in Phase 3."
		}
	case modeVideos:
		if err := yt.OpenURL(i.value); err != nil {
			a.status = "Could not open browser: " + err.Error()
		} else {
			a.status = "Opened in browser: " + i.value
		}
	}
	return a, nil
}

func (a *app) startCategorizing(fresh bool) (tea.Model, tea.Cmd) {
	a.fresh = fresh
	if err := a.aiCfg.RequireKey(); err != nil {
		a.status = err.Error()
		return a, nil
	}
	g, err := a.aiCfg.NewGemini(context.Background())
	if err != nil {
		a.status = err.Error()
		return a, nil
	}

	a.mode = modeCategorizing
	a.progress.Reset()
	a.viewport.SetContent("Waiting for classification...")
	a.viewport.GotoBottom()

	progressCh := make(chan string, 16)
	doneCh := make(chan categoryDoneMsg, 1)
	a.progressCh = progressCh

	go func() {
		songs, err := analyze.Categorize(context.Background(), g, a.client, a.selected, ".", fresh,
			func(format string, args ...any) {
				progressCh <- fmt.Sprintf(format, args...)
			})
		close(progressCh)
		doneCh <- categoryDoneMsg{songs: songs, err: err}
	}()

	return a, tea.Batch(
		waitCategoryProgress(progressCh),
		waitCategoryDone(doneCh),
	)
}

func (a *app) selectedTitle() string {
	if a.selected == nil || a.selected.Snippet == nil {
		return "playlist"
	}
	return a.selected.Snippet.Title
}

func (a *app) onPlaylistsLoaded(msg playlistsLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		a.err = msg.err
		a.status = "Failed to load playlists: " + msg.err.Error()
		return a, tea.Quit
	}
	a.playlists = msg.playlists
	if len(a.playlists) == 0 {
		a.status = "No playlists found for the authenticated account."
	}
	a.mode = modePlaylists
	a.rebuildList(playlistItems(a.playlists), "Your Playlists")
	return a, nil
}

func (a *app) onVideosLoaded(msg videosLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		a.status = "Failed to load videos: " + msg.err.Error()
		a.mode = modeActions
		a.rebuildList(actionItems(), fmt.Sprintf("Actions — %s", a.selectedTitle()))
		return a, nil
	}
	a.mode = modeVideos
	title := fmt.Sprintf("Videos — %s (%d)", a.selectedTitle(), len(msg.items))
	a.rebuildList(videoItems(msg.items), title)
	return a, nil
}

func (a *app) onCategoryDone(msg categoryDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		a.status = "Classification failed: " + msg.err.Error()
		a.mode = modeActions
		a.rebuildList(actionItems(), fmt.Sprintf("Actions — %s", a.selectedTitle()))
		return a, nil
	}
	a.mode = modeReport
	a.report = renderReport(msg.songs)
	a.viewport.SetContent(a.report)
	a.viewport.GotoTop()
	a.status = fmt.Sprintf("Classified %d tracks", len(msg.songs))
	return a, nil
}

func (a *app) rebuildList(items []list.Item, title string) {
	a.list.Title = title
	a.list.SetItems(items)
	a.list.ResetSelected()
	a.list.SetShowFilter(false)
	a.list.SetFilteringEnabled(false)
}

func (a *app) View() string {
	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("205")).
		Width(a.width).
		Align(lipgloss.Center).
		Render("Bubbles — YouTube Playlist Analyzer")

	var body string
	switch a.mode {
	case modeBoot:
		body = lipgloss.NewStyle().Padding(1).Render(a.spinner.View() + " Loading your playlists...")
	case modeLoadingVideos:
		body = lipgloss.NewStyle().Padding(1).Render(a.spinner.View() + " Loading videos...")
	case modePlaylists, modeActions, modeVideos:
		body = a.list.View()
	case modeCategorizing:
		body = lipgloss.NewStyle().Padding(0, 1).Render(a.spinner.View()+" Classifying...") + "\n\n" + a.viewport.View()
	case modeReport:
		body = a.viewport.View()
	}

	status := a.status
	if status != "" {
		status = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render(status)
	}

	return lipgloss.JoinVertical(lipgloss.Left, header, body, status, a.helpText())
}

func (a *app) helpText() string {
	switch a.mode {
	case modePlaylists:
		return "↑/↓ navigate · Enter open playlist · q quit"
	case modeActions:
		return "↑/↓ choose · Enter run · Esc back · q quit"
	case modeVideos:
		return "↑/↓ browse · Enter open video in browser · Esc back · q quit"
	case modeCategorizing:
		return "Classifying... (this can take a minute or two) · ctrl+c quit"
	case modeReport:
		return "↑/↓ scroll · Esc back · q quit"
	}
	return ""
}

func renderReport(songs []analyze.Song) string {
	var sb strings.Builder
	if err := analyze.PrintReport(songs, &sb); err != nil {
		return err.Error()
	}
	return sb.String()
}

func playlistItems(pls []*youtube.Playlist) []list.Item {
	out := make([]list.Item, 0, len(pls))
	for _, p := range pls {
		title := p.Snippet.Title
		if title == "" {
			title = "(untitled)"
		}
		out = append(out, item{title: title, desc: "Open to browse or analyze", value: p.Id})
	}
	return out
}

func actionItems() []list.Item {
	return []list.Item{
		item{title: "View videos", desc: "Browse all songs in this playlist", value: actVideos},
		item{title: "Categorize", desc: "Classify into Party/Love/Workout/Chill/Sad/Other (uses saved cache)", value: actCategorize},
		item{title: "Re-categorize", desc: "Force a fresh classification, ignoring the saved cache", value: actReCat},
		item{title: "Suggest", desc: "Discover songs you're missing (Phase 3)", value: actSuggest},
	}
}

func videoItems(items []*youtube.PlaylistItem) []list.Item {
	out := make([]list.Item, 0, len(items))
	for _, it := range items {
		title := it.Snippet.Title
		if title == "" {
			title = "(untitled)"
		}
		url := "https://www.youtube.com/watch?v=" + it.Snippet.ResourceId.VideoId
		out = append(out, item{title: title, desc: url, value: url})
	}
	return out
}

type playlistsLoadedMsg struct {
	playlists []*youtube.Playlist
	err       error
}

type videosLoadedMsg struct {
	items []*youtube.PlaylistItem
	err   error
}

type categoryProgressMsg struct {
	line string
}

type categoryDoneMsg struct {
	songs []analyze.Song
	err   error
}

func loadPlaylistsCmd(cl *yt.Client) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		pls, err := cl.Playlists(ctx)
		return playlistsLoadedMsg{playlists: pls, err: err}
	}
}

func loadVideosCmd(cl *yt.Client, playlistID string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		items, err := cl.PlaylistVideos(ctx, playlistID)
		return videosLoadedMsg{items: items, err: err}
	}
}

func waitCategoryProgress(ch <-chan string) tea.Cmd {
	return func() tea.Msg {
		line, ok := <-ch
		if !ok {
			return nil
		}
		return categoryProgressMsg{line: line}
	}
}

func waitCategoryDone(ch <-chan categoryDoneMsg) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}

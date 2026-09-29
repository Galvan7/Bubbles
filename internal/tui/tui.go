package tui

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
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
	modeSuggesting
	modeReport
	modeCreating
	modePicking
	modeNamePrompt
)

const (
	actVideos     = "videos"
	actCategorize = "categorize"
	actReCat      = "recategorize"
	actSuggest    = "suggest"
	actPick       = "pick"
)

type item struct {
	title   string
	desc    string
	value   string
	videoID string
	checked bool
}

func (i item) Title() string       { return i.title }
func (i item) Description() string  { return i.desc }
func (i item) FilterValue() string { return i.title }

// checkDelegate renders list items with a checkbox reflecting the checked set.
type checkDelegate struct {
	base    list.DefaultDelegate
	checked map[string]bool
}

func newCheckDelegate() *checkDelegate {
	return &checkDelegate{base: list.NewDefaultDelegate(), checked: map[string]bool{}}
}

func (d *checkDelegate) Height() int                             { return d.base.Height() }
func (d *checkDelegate) Spacing() int                            { return d.base.Spacing() }
func (d *checkDelegate) Update(msg tea.Msg, m *list.Model) tea.Cmd { return d.base.Update(msg, m) }

func (d *checkDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	it, ok := listItem.(item)
	if !ok {
		d.base.Render(w, m, index, listItem)
		return
	}
	box := "[ ] "
	if d.checked[it.videoID] {
		box = "[x] "
	}
	// Prefix the checkbox onto the title and delegate the rest of the rendering.
	it.title = box + it.title
	d.base.Render(w, m, index, it)
}

func (d *checkDelegate) count() int {
	n := 0
	for _, v := range d.checked {
		if v {
			n++
		}
	}
	return n
}

// chosenInOrder returns the checked items in their original list order.
func chosenInOrder(items []list.Item, checked map[string]bool) []item {
	var out []item
	for _, li := range items {
		if it, ok := li.(item); ok && checked[it.videoID] {
			out = append(out, it)
		}
	}
	return out
}

type app struct {
	client    *yt.Client
	aiCfg     *ai.Config
	playlists []*youtube.Playlist
	selected  *youtube.Playlist

	mode       mode
	spinner    spinner.Model
	list       list.Model
	viewport   viewport.Model
	nameInput  textinput.Model
	pickDelegate *checkDelegate
	pickNextToken string
	pickTotal     int64
	pickLoaded    int
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
	cfg := ai.NewConfig()
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
	case pickerLoadedMsg:
		return a.onPickerLoaded(msg)
	case categoryProgressMsg:
		a.progress.WriteString(msg.line + "\n")
		a.viewport.SetContent(a.progress.String())
		a.viewport.GotoBottom()
		return a, waitCategoryProgress(a.progressCh)
	case categoryDoneMsg:
		return a.onCategoryDone(msg)
	case suggestDoneMsg:
		return a.onSuggestDone(msg)
	case rangeDoneMsg:
		return a.onRangeDone(msg)
	}
	return a, nil
}

func (a *app) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Picker: space toggles the highlighted item, c creates, esc goes back.
	if a.mode == modePicking {
		// Let the list handle keys while the filter box is open.
		if a.list.FilterState() == list.Filtering {
			var cmd tea.Cmd
			a.list, cmd = a.list.Update(msg)
			return a, cmd
		}
		switch msg.String() {
		case "ctrl+c":
			return a, tea.Quit
		case "esc":
			a.mode = modeActions
			a.pickDelegate = nil
			a.rebuildList(actionItems(), fmt.Sprintf("Actions — %s", a.selectedTitle()))
			a.status = ""
			return a, nil
		case " ":
			if it, ok := a.list.SelectedItem().(item); ok && it.videoID != "" {
				a.pickDelegate.checked[it.videoID] = !a.pickDelegate.checked[it.videoID]
				a.status = fmt.Sprintf("%d selected", a.pickDelegate.count())
			}
			return a, nil
		case "c", "enter":
			if a.pickDelegate.count() == 0 {
				a.status = "Nothing selected yet. Use Space to tick videos."
				return a, nil
			}
			return a.openNamePrompt()
		case "m":
			if a.pickNextToken == "" {
				a.status = fmt.Sprintf("All %d videos loaded.", a.pickLoaded)
				return a, nil
			}
			a.status = "Loading more..."
			return a, loadPickerPageCmd(a.client, a.selected.Id, a.pickNextToken)
		}
		var cmd tea.Cmd
		a.list, cmd = a.list.Update(msg)
		return a, cmd
	}

	// Name prompt: type the new playlist's name.
	if a.mode == modeNamePrompt {
		switch msg.String() {
		case "ctrl+c":
			return a, tea.Quit
		case "esc":
			a.mode = modePicking
			a.status = fmt.Sprintf("%d selected", a.pickDelegate.count())
			return a, nil
		case "enter":
			name := strings.TrimSpace(a.nameInput.Value())
			if name == "" {
				a.status = "Please enter a name."
				return a, nil
			}
			return a.startCreateFromSelection(name)
		}
		var cmd tea.Cmd
		a.nameInput, cmd = a.nameInput.Update(msg)
		return a, cmd
	}

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

	case modeCategorizing, modeSuggesting, modeCreating:
		if msg.String() == "esc" {
			a.status = "Still running in the background. Esc again once it finishes."
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
			return a.startSuggesting()
		case actPick:
			a.mode = modeLoadingVideos
			a.pickNextToken = ""
			a.pickTotal = 0
			a.pickLoaded = 0
			a.pickDelegate = newCheckDelegate()
			return a, loadPickerPageCmd(a.client, a.selected.Id, "")
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

func (a *app) openNamePrompt() (tea.Model, tea.Cmd) {
	ti := textinput.New()
	ti.Placeholder = fmt.Sprintf("%s (picks)", a.selectedTitle())
	ti.SetValue(fmt.Sprintf("%s (picks)", a.selectedTitle()))
	ti.Focus()
	ti.CharLimit = 100
	ti.Width = 40
	a.nameInput = ti
	a.mode = modeNamePrompt
	a.status = ""
	return a, textinput.Blink
}

func (a *app) startCreateFromSelection(name string) (tea.Model, tea.Cmd) {
	if a.selected == nil || a.pickDelegate == nil {
		a.status = "Nothing to create."
		return a, nil
	}

	// Snapshot the chosen video IDs, preserving the playlist's original order.
	chosen := chosenInOrder(a.list.Items(), a.pickDelegate.checked)

	a.mode = modeCreating
	a.progress.Reset()
	a.viewport.SetContent("Creating your playlist...")
	a.viewport.GotoBottom()

	progressCh := make(chan string, 16)
	doneCh := make(chan rangeDoneMsg, 1)
	a.progressCh = progressCh
	progressFn := func(format string, args ...any) {
		progressCh <- fmt.Sprintf(format, args...)
	}

	client := a.client
	sourceTitle := a.selectedTitle()

	go func() {
		defer close(progressCh)
		ctx := context.Background()

		progressFn("Creating new playlist %q...", name)
		created, err := client.CreatePlaylist(ctx, name,
			fmt.Sprintf("Created by Bubbles from selected videos in %q.", sourceTitle))
		if err != nil {
			doneCh <- rangeDoneMsg{err: err}
			return
		}

		added := 0
		for _, it := range chosen {
			if it.videoID == "" {
				continue
			}
			if err := client.AddToPlaylist(ctx, created.Id, it.videoID); err != nil {
				progressFn("Failed to add %q: %v", it.title, err)
				continue
			}
			added++
			progressFn("Added (%d/%d): %s", added, len(chosen), it.title)
		}

		doneCh <- rangeDoneMsg{title: name, playlistID: created.Id, added: added, err: nil}
	}()

	return a, tea.Batch(
		waitCategoryProgress(progressCh),
		waitRangeDone(doneCh),
	)
}

func (a *app) startSuggesting() (tea.Model, tea.Cmd) {
	if err := a.aiCfg.RequireKey(); err != nil {
		a.status = err.Error()
		return a, nil
	}
	g, err := a.aiCfg.NewGemini(context.Background())
	if err != nil {
		a.status = err.Error()
		return a, nil
	}

	a.mode = modeSuggesting
	a.progress.Reset()
	a.viewport.SetContent("Preparing suggestions...")
	a.viewport.GotoBottom()

	progressCh := make(chan string, 16)
	doneCh := make(chan suggestDoneMsg, 1)
	a.progressCh = progressCh
	progressFn := func(format string, args ...any) {
		progressCh <- fmt.Sprintf(format, args...)
	}

	go func() {
		songs, err := analyze.Categorize(context.Background(), g, a.client, a.selected, ".", false, progressFn)
		if err == nil {
			progressFn("Generating song suggestions with Gemini...")
			recs, jerr := analyze.Suggest(context.Background(), g, songs)
			doneCh <- suggestDoneMsg{recs: recs, err: jerr}
		} else {
			doneCh <- suggestDoneMsg{err: err}
		}
		close(progressCh)
	}()

	return a, tea.Batch(
		waitCategoryProgress(progressCh),
		waitSuggestDone(doneCh),
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

func (a *app) onPickerLoaded(msg pickerLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		a.status = "Failed to load videos: " + msg.err.Error()
		a.mode = modeActions
		a.rebuildList(actionItems(), fmt.Sprintf("Actions — %s", a.selectedTitle()))
		return a, nil
	}

	newItems := make([]list.Item, 0, len(msg.items))
	for _, it := range msg.items {
		title := it.Snippet.Title
		if title == "" {
			title = "(untitled)"
		}
		vid := ""
		if it.Snippet.ResourceId != nil {
			vid = it.Snippet.ResourceId.VideoId
		}
		newItems = append(newItems, item{title: title, desc: "Space to select", videoID: vid})
	}

	a.pickNextToken = msg.nextToken
	if msg.total > 0 {
		a.pickTotal = msg.total
	}

	firstPage := a.mode != modePicking
	if firstPage {
		// First page: build the list fresh.
		a.mode = modePicking
		if a.pickDelegate == nil {
			a.pickDelegate = newCheckDelegate()
		}
		a.list = list.New(newItems, a.pickDelegate, a.width, a.height-3)
		a.list.SetFilteringEnabled(true)
		a.list.SetShowFilter(true)
	} else {
		// Subsequent pages: append to what's already loaded.
		existing := a.list.Items()
		a.list.SetItems(append(existing, newItems...))
	}

	a.pickLoaded = len(a.list.Items())
	a.list.Title = fmt.Sprintf("Pick videos — %s (%d/%d loaded)", a.selectedTitle(), a.pickLoaded, a.pickTotal)

	if a.pickNextToken == "" {
		a.status = fmt.Sprintf("All %d videos loaded · %d selected", a.pickLoaded, a.pickDelegate.count())
	} else {
		a.status = fmt.Sprintf("%d/%d loaded · press m for more · %d selected", a.pickLoaded, a.pickTotal, a.pickDelegate.count())
	}
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

func (a *app) onSuggestDone(msg suggestDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		a.status = "Suggestions failed: " + msg.err.Error()
		a.mode = modeActions
		a.rebuildList(actionItems(), fmt.Sprintf("Actions — %s", a.selectedTitle()))
		return a, nil
	}
	a.mode = modeReport
	a.report = renderRecommendations(msg.recs)
	a.viewport.SetContent(a.report)
	a.viewport.GotoTop()
	a.status = fmt.Sprintf("Recommended %d songs", len(msg.recs))
	return a, nil
}

func (a *app) onRangeDone(msg rangeDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		a.status = "Create playlist failed: " + msg.err.Error()
		a.mode = modeActions
		a.rebuildList(actionItems(), fmt.Sprintf("Actions — %s", a.selectedTitle()))
		return a, nil
	}
	a.mode = modeReport
	url := "https://www.youtube.com/playlist?list=" + msg.playlistID
	a.report = fmt.Sprintf(
		"Created playlist: %s\n\nAdded %d videos.\n\nOpen it here:\n%s\n\n(Press Esc to go back.)",
		msg.title, msg.added, url,
	)
	a.viewport.SetContent(a.report)
	a.viewport.GotoTop()
	a.status = fmt.Sprintf("Created %q with %d videos", msg.title, msg.added)
	return a, nil
}

func (a *app) rebuildList(items []list.Item, title string) {
	a.list = list.New(items, list.NewDefaultDelegate(), a.width, a.height-3)
	a.list.Title = title
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
	case modePlaylists, modeActions, modeVideos, modePicking:
		body = a.list.View()
	case modeCategorizing:
		body = lipgloss.NewStyle().Padding(0, 1).Render(a.spinner.View()+" Classifying...") + "\n\n" + a.viewport.View()
	case modeSuggesting:
		body = lipgloss.NewStyle().Padding(0, 1).Render(a.spinner.View()+" Generating suggestions...") + "\n\n" + a.viewport.View()
	case modeCreating:
		body = lipgloss.NewStyle().Padding(0, 1).Render(a.spinner.View()+" Creating playlist...") + "\n\n" + a.viewport.View()
	case modeNamePrompt:
		n := 0
		if a.pickDelegate != nil {
			n = a.pickDelegate.count()
		}
		prompt := fmt.Sprintf("Name for the new playlist (%d videos):", n)
		body = lipgloss.NewStyle().Padding(1).Render(prompt + "\n\n" + a.nameInput.View())
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
	case modeSuggesting:
		return "Generating suggestions... · ctrl+c quit"
	case modeCreating:
		return "Creating playlist... · ctrl+c quit"
	case modePicking:
		return "↑/↓ move · Space select · m load 50 more · / filter · c create · Esc back · ctrl+c quit"
	case modeNamePrompt:
		return "Type a name · Enter create · Esc back · ctrl+c quit"
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

func renderRecommendations(recs []analyze.Recommendation) string {
	var sb strings.Builder
	if err := analyze.PrintRecommendations(recs, &sb); err != nil {
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
		item{title: "Suggest", desc: "Discover songs you're missing based on this playlist", value: actSuggest},
		item{title: "Pick videos → new playlist", desc: "Hand-pick videos with Space, then create a new playlist from them", value: actPick},
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

type pickerLoadedMsg struct {
	items     []*youtube.PlaylistItem
	nextToken string
	total     int64
	err       error
}

type categoryProgressMsg struct {
	line string
}

type categoryDoneMsg struct {
	songs []analyze.Song
	err   error
}

type suggestDoneMsg struct {
	recs []analyze.Recommendation
	err  error
}

type rangeDoneMsg struct {
	title      string
	playlistID string
	added      int
	err        error
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

func loadPickerPageCmd(cl *yt.Client, playlistID, pageToken string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		items, next, total, err := cl.PlaylistVideosPage(ctx, playlistID, pageToken, 50)
		return pickerLoadedMsg{items: items, nextToken: next, total: total, err: err}
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

func waitSuggestDone(ch <-chan suggestDoneMsg) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}

func waitRangeDone(ch <-chan rangeDoneMsg) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}

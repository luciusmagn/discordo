package history

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
	"github.com/ayn2op/arikawa/v3/utils/handler"
	"github.com/ayn2op/ningen/v3"
)

type messageAPI interface {
	MessagesBefore(discord.ChannelID, discord.MessageID, uint) ([]discord.Message, error)
	MessagesAfter(discord.ChannelID, discord.MessageID, uint) ([]discord.Message, error)
}

// Client keeps downloaded history and gateway messages in an account-specific database.
// Page coverage distinguishes complete history from isolated messages received while away.
type Client struct {
	mu        sync.Mutex
	state     *ningen.State
	api       messageAPI
	directory string
	store     *Store
	account   discord.UserID
	closed    bool
	lastErr   error
	incoming  map[discord.ChannelID]discord.MessageID
}

// NewClient attaches storage before the gateway dispatches messages to the UI.
// An empty directory selects the user's application data directory.
func NewClient(state *ningen.State, directory string) *Client {
	c := &Client{state: state, api: state.Session, directory: directory}
	if state.PreHandler == nil {
		state.PreHandler = handler.New()
	}
	state.PreHandler.AddSyncHandler(c.beforeEvent)
	state.State.AddSyncHandler(c.afterEvent)
	return c
}

func dataDirectory() (string, error) {
	if runtime.GOOS == "windows" {
		root, err := os.UserConfigDir()
		return filepath.Join(root, "discordo", "messages"), err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "discordo", "messages"), nil
	}
	root := os.Getenv("XDG_DATA_HOME")
	if !filepath.IsAbs(root) {
		root = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(root, "discordo", "messages"), nil
}

func (c *Client) openAccount(id discord.UserID) error {
	if !id.IsValid() {
		return fmt.Errorf("missing account ID")
	}
	if c.account == id && c.store != nil {
		return nil
	}
	if c.store != nil {
		if err := c.store.Close(); err != nil {
			return err
		}
		c.store = nil
	}
	dir := c.directory
	if dir == "" {
		var err error
		dir, err = dataDirectory()
		if err != nil {
			return err
		}
	}
	store, err := Open(filepath.Join(dir, id.String()+".db"))
	if err != nil {
		return err
	}
	c.store, c.account = store, id
	c.lastErr = nil
	return nil
}

func (c *Client) report(err error) {
	if err != nil {
		c.lastErr = err
		slog.Error("message cache", "err", err)
	}
}

// Err reports storage failure for the UI; live messaging can continue without the cache.
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastErr
}

func (c *Client) beforeEvent(event gateway.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	if ready, ok := event.(*gateway.ReadyEvent); ok {
		c.incoming = make(map[discord.ChannelID]discord.MessageID)
		c.report(c.openAccount(ready.User.ID))
		return
	}
	if c.store == nil {
		return
	}
	switch event := event.(type) {
	case *gateway.MessageCreateEvent:
		if err := c.saveIncoming(event.Message); err != nil {
			c.report(err)
		} else {
			c.incoming[event.ChannelID] = max(c.incoming[event.ChannelID], event.ID)
		}
	case *gateway.MessageDeleteEvent:
		c.report(c.store.Delete(event.ChannelID, []discord.MessageID{event.ID}))
	case *gateway.MessageDeleteBulkEvent:
		c.report(c.store.Delete(event.ChannelID, event.IDs))
	}
}

// saveIncoming extends a known tail only if the gateway's previous head matches it.
// After a disconnect, an isolated notification cannot conceal missing history.
func (c *Client) saveIncoming(message discord.Message) error {
	channel, channelErr := c.state.Cabinet.Channel(message.ChannelID)
	prefix, complete, err := c.store.CachedPage(message.ChannelID, 0, 100)
	if err != nil {
		return err
	}
	newest, err := c.store.MessagesBefore(message.ChannelID, 0, 1)
	if err != nil {
		return err
	}
	if len(newest) > 0 && message.ID <= newest[0].ID {
		return c.store.Put([]discord.Message{message})
	}
	previousMatches := channelErr == nil && len(prefix) > 0 && channel.LastMessageID == prefix[0].ID
	exhausted := previousMatches && complete && len(prefix) < 100
	if !previousMatches {
		exhausted = channelErr == nil && complete && len(prefix) == 0 && !channel.LastMessageID.IsValid()
		prefix = nil
	}
	page := append([]discord.Message{message}, prefix...)
	if len(page) > 100 {
		page = page[:100]
		exhausted = false
	}
	limit := uint(len(page))
	if exhausted {
		limit++
	}
	return c.store.SavePage(message.ChannelID, 0, limit, page)
}

func (c *Client) afterEvent(event gateway.Event) {
	update, ok := event.(*gateway.MessageUpdateEvent)
	if !ok {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.store == nil {
		return
	}
	// The upstream state merges partial updates. Ignore edits outside its memory window.
	if message, err := c.state.Cabinet.Message(update.ChannelID, update.ID); err == nil {
		c.report(c.store.Put([]discord.Message{*message}))
	}
}

func (c *Client) Messages(channelID discord.ChannelID, limit uint) ([]discord.Message, error) {
	return c.MessagesBefore(channelID, 0, limit)
}

// MessagesBefore fetches only the missing suffix of a certified cached page.
func (c *Client) MessagesBefore(channelID discord.ChannelID, before discord.MessageID, limit uint) ([]discord.Message, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errSessionChanged
	}
	limit = max(1, min(limit, 100))
	initialIncoming := c.incoming[channelID]
	var prefix []discord.Message
	var complete bool
	if c.store != nil {
		var err error
		prefix, complete, err = c.store.CachedPage(channelID, before, limit)
		c.report(err)
		if err != nil {
			prefix, complete = nil, false
		}
		if before == 0 {
			channel, err := c.state.Cabinet.Channel(channelID)
			if err != nil || (len(prefix) == 0 && channel.LastMessageID.IsValid()) || (len(prefix) > 0 && prefix[0].ID != channel.LastMessageID) {
				// Fetch newer messages after a reconnect without downloading the old tail again.
				if err == nil && len(prefix) > 0 && channel.LastMessageID > prefix[0].ID {
					newer, fetchErr := c.request(channelID, prefix[0].ID, 100, true)
					if errors.Is(fetchErr, errSessionChanged) {
						return nil, fetchErr
					}
					if fetchErr == nil && len(newer) > 0 && newer[0].ID >= channel.LastMessageID {
						combined := append(newer, prefix...)
						requested := uint(len(combined))
						if complete && uint(len(prefix)) < limit {
							requested++
						}
						c.saveFetched(channelID, 0, requested, combined)
						if uint(len(combined)) >= limit {
							combined = combined[:limit]
							complete = true
						}
						combined = c.includeIncoming(channelID, before, limit, initialIncoming, combined)
						c.remember(combined)
						if complete {
							return combined, nil
						}
						prefix = combined
					} else {
						prefix, complete = nil, false
					}
				} else {
					prefix, complete = nil, false
				}
				// Reuse even a single cached notification if it is the current head.
				if len(prefix) == 0 && err == nil && channel.LastMessageID.IsValid() {
					if message, err := c.store.Message(channelID, channel.LastMessageID); err == nil && message != nil {
						prefix = []discord.Message{*message}
						complete = limit == 1
					}
				}
			}
		}
	}
	if complete || uint(len(prefix)) >= limit {
		c.remember(prefix)
		return prefix, nil
	}
	anchor := before
	if len(prefix) > 0 {
		anchor = prefix[len(prefix)-1].ID
	}
	remaining := limit - uint(len(prefix))
	messages, err := c.request(channelID, anchor, remaining, false)
	if errors.Is(err, errSessionChanged) {
		return nil, err
	}
	if err != nil {
		if c.store != nil {
			cached, cacheErr := c.store.MessagesBefore(channelID, before, limit)
			if cacheErr == nil && len(cached) > 0 {
				slog.Warn("showing cached history after fetch failure", "channel_id", channelID, "err", err)
				c.remember(cached)
				return cached, nil
			}
		}
		return nil, err
	}
	if anchor != before {
		c.saveFetched(channelID, anchor, remaining, messages)
	}
	messages = append(prefix, messages...)
	c.saveFetched(channelID, before, limit, messages)
	messages = c.includeIncoming(channelID, before, limit, initialIncoming, messages)
	c.remember(messages)
	return messages, nil
}

// Prefer the certified live tail if a new message arrived during this latest-page request.
// Only arrivals during this request qualify, so stale disk heads cannot replace HTTP results.
func (c *Client) includeIncoming(channelID discord.ChannelID, before discord.MessageID, limit uint, initial discord.MessageID, messages []discord.Message) []discord.Message {
	if before != 0 || c.store == nil || c.incoming[channelID] <= initial {
		return messages
	}
	latest, _, err := c.store.CachedPage(channelID, 0, limit)
	if err == nil && len(latest) > 0 && latest[0].ID == c.incoming[channelID] && (len(messages) == 0 || latest[0].ID > messages[0].ID) {
		return latest
	}
	return messages
}

var errSessionChanged = errors.New("message cache session closed or changed")

// Release the storage lock during HTTP, so incoming notifications can be committed immediately.
func (c *Client) request(channelID discord.ChannelID, anchor discord.MessageID, limit uint, after bool) ([]discord.Message, error) {
	account, store := c.account, c.store
	c.mu.Unlock()
	var messages []discord.Message
	var err error
	if after {
		messages, err = c.api.MessagesAfter(channelID, anchor, limit)
	} else {
		messages, err = c.api.MessagesBefore(channelID, anchor, limit)
	}
	c.mu.Lock()
	if c.closed || c.account != account || c.store != store {
		return nil, errSessionChanged
	}
	return messages, err
}

func (c *Client) saveFetched(channelID discord.ChannelID, before discord.MessageID, limit uint, messages []discord.Message) {
	if c.store == nil {
		return
	}
	if before == 0 {
		// Preserve a live arrival while HTTP is in flight, not a stale disk head.
		latest, _, err := c.store.CachedPage(channelID, 0, 1)
		if err == nil && len(latest) > 0 && latest[0].ID == c.incoming[channelID] {
			if len(messages) == 0 {
				return
			}
			if latest[0].ID > messages[0].ID {
				before = messages[0].ID + 1
			}
		}
	}
	c.report(c.store.SavePage(channelID, before, limit, messages))
}

func (c *Client) remember(messages []discord.Message) {
	for i := range messages {
		messages[i].Reactions = nil
		if err := c.state.Cabinet.MessageSet(&messages[i], false); err != nil {
			slog.Warn("could not populate message state", "err", err)
		}
	}
	if len(messages) > 0 {
		channel, err := c.state.Cabinet.Channel(messages[0].ChannelID)
		if err == nil && messages[0].ID > channel.LastMessageID {
			updated := *channel
			updated.LastMessageID = messages[0].ID
			if err := c.state.Cabinet.ChannelSet(&updated, false); err != nil {
				slog.Warn("could not update channel head", "err", err)
			}
		}
	}
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	if c.store == nil {
		return nil
	}
	err := c.store.Close()
	c.store = nil
	return err
}

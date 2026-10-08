package history

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
	"github.com/ayn2op/ningen/v3"
)

type fetchCall struct {
	after  bool
	anchor discord.MessageID
	limit  uint
}

type fakeAPI struct {
	mu       sync.Mutex
	messages []discord.Message
	calls    []fetchCall
	err      error
	started  chan struct{}
	release  chan struct{}
}

func (a *fakeAPI) fetch(anchor discord.MessageID, limit uint, after bool) ([]discord.Message, error) {
	if limit == 0 {
		panic("unbounded history request")
	}
	a.mu.Lock()
	a.calls = append(a.calls, fetchCall{after: after, anchor: anchor, limit: limit})
	messages, err := a.messages, a.err
	a.mu.Unlock()
	if a.started != nil {
		close(a.started)
		<-a.release
	}
	var result []discord.Message
	for _, message := range messages {
		if after && message.ID > anchor || !after && (anchor == 0 || message.ID < anchor) {
			result = append(result, message)
		}
	}
	if uint(len(result)) > limit {
		if after {
			result = result[len(result)-int(limit):]
		} else {
			result = result[:limit]
		}
	}
	return result, err
}
func (a *fakeAPI) MessagesBefore(_ discord.ChannelID, anchor discord.MessageID, limit uint) ([]discord.Message, error) {
	return a.fetch(anchor, limit, false)
}
func (a *fakeAPI) MessagesAfter(_ discord.ChannelID, anchor discord.MessageID, limit uint) ([]discord.Message, error) {
	return a.fetch(anchor, limit, true)
}
func (a *fakeAPI) callCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.calls)
}

func historyClient(t *testing.T, directory string, head discord.MessageID, api *fakeAPI) *Client {
	t.Helper()
	state := ningen.New("")
	if err := state.Cabinet.MyselfSet(discord.User{ID: 1}, false); err != nil {
		t.Fatal(err)
	}
	if err := state.Cabinet.ChannelSet(&discord.Channel{ID: 1, Type: discord.DirectMessage, DMRecipients: []discord.User{{ID: 2}}, LastMessageID: head}, false); err != nil {
		t.Fatal(err)
	}
	client := NewClient(state, directory)
	client.beforeEvent(&gateway.ReadyEvent{User: discord.User{ID: 1}})
	if err := client.Err(); err != nil {
		t.Fatal(err)
	}
	client.api = api
	t.Cleanup(func() { client.Close() })
	return client
}

func historyRows(ids ...discord.MessageID) []discord.Message {
	rows := make([]discord.Message, len(ids))
	for i, id := range ids {
		rows[i] = discord.Message{ID: id, ChannelID: 1, Author: discord.User{ID: 2}, Content: id.String()}
	}
	return rows
}

func requireIDs(t *testing.T, messages []discord.Message, err error, want ...discord.MessageID) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	var ids []discord.MessageID
	for _, message := range messages {
		ids = append(ids, message.ID)
	}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("message IDs = %v, want %v", ids, want)
	}
}

func TestHistoryReuseAcrossRestartAndPageSizes(t *testing.T) {
	directory := t.TempDir()
	api := &fakeAPI{messages: historyRows(10, 9, 8, 7, 6, 5)}
	client := historyClient(t, directory, 10, api)
	rows, err := client.Messages(1, 2)
	requireIDs(t, rows, err, 10, 9)
	rows, err = client.MessagesBefore(1, 9, 3)
	requireIDs(t, rows, err, 8, 7, 6)
	rows, err = client.MessagesBefore(1, 8, 2)
	requireIDs(t, rows, err, 7, 6)
	if api.callCount() != 2 {
		t.Fatalf("history requests = %d", api.callCount())
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	client = historyClient(t, directory, 10, api)
	rows, err = client.Messages(1, 5)
	requireIDs(t, rows, err, 10, 9, 8, 7, 6)
	if api.callCount() != 2 {
		t.Fatalf("restart redownloaded history: %d calls", api.callCount())
	}
}

func TestGatewayNotificationsAreCachedBeforeDisplay(t *testing.T) {
	directory := t.TempDir()
	api := &fakeAPI{messages: historyRows(11, 10, 9)}
	client := historyClient(t, directory, 10, api)
	client.state.Session.Handler.Call(&gateway.MessageCreateEvent{Message: historyRows(11)[0]})
	stored, err := client.store.Message(1, 11)
	if err != nil || stored == nil || stored.Content != "11" {
		t.Fatalf("incoming message not committed: %v, %v", stored, err)
	}
	rows, err := client.Messages(1, 3)
	requireIDs(t, rows, err, 11, 10, 9)
	if api.callCount() != 1 || api.calls[0].anchor != 11 || api.calls[0].limit != 2 {
		t.Fatalf("notification was redownloaded: %+v", api.calls)
	}
	client.Close()
	client = historyClient(t, directory, 11, api)
	rows, err = client.Messages(1, 3)
	requireIDs(t, rows, err, 11, 10, 9)
	if api.callCount() != 1 {
		t.Fatal("reopened cache fetched the notification again")
	}
}

func TestNotificationWithUnknownHeadDoesNotHideHistory(t *testing.T) {
	api := &fakeAPI{messages: historyRows(11, 10, 9)}
	client := historyClient(t, t.TempDir(), 0, api)
	client.state.Session.Handler.Call(&gateway.MessageCreateEvent{Message: historyRows(11)[0]})
	rows, err := client.Messages(1, 3)
	requireIDs(t, rows, err, 11, 10, 9)
	if api.callCount() != 1 || api.calls[0].anchor != 11 || api.calls[0].limit != 2 {
		t.Fatalf("unknown head concealed history or redownloaded notification: %+v", api.calls)
	}
}

func TestFetchedHistoryUpdatesUnknownChannelHead(t *testing.T) {
	api := &fakeAPI{messages: historyRows(11, 10, 9)}
	client := historyClient(t, t.TempDir(), 0, api)
	rows, err := client.Messages(1, 3)
	requireIDs(t, rows, err, 11, 10, 9)
	rows, err = client.Messages(1, 3)
	requireIDs(t, rows, err, 11, 10, 9)
	if api.callCount() != 1 {
		t.Fatalf("unknown head caused repeat fetch: %+v", api.calls)
	}
}

func TestReconnectFetchesOnlyNewerMessages(t *testing.T) {
	directory := t.TempDir()
	api := &fakeAPI{messages: historyRows(5, 4, 3)}
	client := historyClient(t, directory, 5, api)
	rows, err := client.Messages(1, 3)
	requireIDs(t, rows, err, 5, 4, 3)
	client.Close()
	api.messages = historyRows(7, 6, 5, 4, 3)
	client = historyClient(t, directory, 7, api)
	rows, err = client.Messages(1, 3)
	requireIDs(t, rows, err, 7, 6, 5)
	if api.callCount() != 2 || !api.calls[1].after || api.calls[1].anchor != 5 {
		t.Fatalf("reconnect requests = %+v", api.calls)
	}
}

func TestStaleDiskHeadIsReplacedByFetchedHistory(t *testing.T) {
	api := &fakeAPI{messages: historyRows(10, 9)}
	client := historyClient(t, t.TempDir(), 10, api)
	if err := client.store.SavePage(1, 0, 1, historyRows(20)); err != nil {
		t.Fatal(err)
	}
	rows, err := client.Messages(1, 2)
	requireIDs(t, rows, err, 10, 9)
	rows, err = client.Messages(1, 2)
	requireIDs(t, rows, err, 10, 9)
	if api.callCount() != 1 {
		t.Fatalf("stale disk head forced another download: %+v", api.calls)
	}
}

func TestCachedMessagesDoNotHideUnknownGaps(t *testing.T) {
	api := &fakeAPI{messages: historyRows(10, 9, 8, 7, 6, 5)}
	client := historyClient(t, t.TempDir(), 10, api)
	if err := client.store.SavePage(1, 0, 1, historyRows(10)); err != nil {
		t.Fatal(err)
	}
	if err := client.store.SavePage(1, 7, 2, historyRows(6, 5)); err != nil {
		t.Fatal(err)
	}
	rows, err := client.Messages(1, 4)
	requireIDs(t, rows, err, 10, 9, 8, 7)
	if api.callCount() != 1 || api.calls[0].anchor != 10 {
		t.Fatalf("missing gap was not fetched: %+v", api.calls)
	}
}

func TestIncomingMessagesPersistDuringSlowHistoryFetch(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "history", true: "empty"}[empty], func(t *testing.T) {
			api := &fakeAPI{started: make(chan struct{}), release: make(chan struct{})}
			var head discord.MessageID
			if !empty {
				api.messages, head = historyRows(10, 9), 10
			}
			client := historyClient(t, t.TempDir(), head, api)
			finished := make(chan error, 1)
			var rows []discord.Message
			go func() {
				var err error
				rows, err = client.Messages(1, 2)
				finished <- err
			}()
			<-api.started
			client.state.Session.Handler.Call(&gateway.MessageCreateEvent{Message: historyRows(11)[0]})
			message, err := client.store.Message(1, 11)
			if err != nil || message == nil {
				t.Fatalf("notification blocked behind HTTP: %v", err)
			}
			close(api.release)
			err = <-finished
			if empty {
				requireIDs(t, rows, err, 11)
			} else {
				requireIDs(t, rows, err, 11, 10)
			}
			prefix, _, err := client.store.CachedPage(1, 0, 1)
			requireIDs(t, prefix, err, 11)
		})
	}
}

func TestAccountChangeDiscardsInflightHistory(t *testing.T) {
	api := &fakeAPI{messages: historyRows(10), started: make(chan struct{}), release: make(chan struct{})}
	client := historyClient(t, t.TempDir(), 10, api)
	finished := make(chan error, 1)
	go func() { _, err := client.Messages(1, 1); finished <- err }()
	<-api.started
	client.beforeEvent(&gateway.ReadyEvent{User: discord.User{ID: 2}})
	close(api.release)
	if err := <-finished; !errors.Is(err, errSessionChanged) {
		t.Fatalf("inflight fetch error = %v", err)
	}
	message, err := client.store.Message(1, 10)
	if err != nil || message != nil {
		t.Fatalf("another account's history was cached: %v, %v", message, err)
	}
}

func TestIncomingTailPreservesOlderCoverage(t *testing.T) {
	api := &fakeAPI{}
	client := historyClient(t, t.TempDir(), 100, api)
	rows := make([]discord.Message, 100)
	for i := range rows {
		rows[i] = historyRows(discord.MessageID(100 - i))[0]
	}
	if err := client.store.SavePage(1, 0, 100, rows); err != nil {
		t.Fatal(err)
	}
	client.state.Session.Handler.Call(&gateway.MessageCreateEvent{Message: historyRows(101)[0]})
	got, err := client.MessagesBefore(1, 2, 1)
	requireIDs(t, got, err, 1)
	if api.callCount() != 0 {
		t.Fatal("durable history was redownloaded after the live tail grew")
	}
}

func TestDisconnectedNotificationPreservesOlderCoverage(t *testing.T) {
	api := &fakeAPI{}
	client := historyClient(t, t.TempDir(), 20, api)
	if err := client.store.SavePage(1, 0, 3, historyRows(10, 9, 8)); err != nil {
		t.Fatal(err)
	}
	client.state.Session.Handler.Call(&gateway.MessageCreateEvent{Message: historyRows(21)[0]})
	got, err := client.MessagesBefore(1, 10, 2)
	requireIDs(t, got, err, 9, 8)
	if api.callCount() != 0 {
		t.Fatal("isolated notification erased older history coverage")
	}
}

package history

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ayn2op/arikawa/v3/discord"
)

func msg(ch discord.ChannelID, id discord.MessageID) discord.Message {
	return discord.Message{ChannelID: ch, ID: id, Content: id.String(), Reactions: []discord.Reaction{{Count: 99}}}
}
func TestStoreDurabilityAndIsolation(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a", "messages.db")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Put([]discord.Message{msg(1, 3), msg(1, 1), msg(1, 2), msg(2, 9)}); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0600 {
		t.Fatalf("mode %o", st.Mode().Perm())
	}
	s, err = Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.MessagesBefore(1, 0, 10)
	if err != nil || len(got) != 3 {
		t.Fatalf("messages=%v err=%v", got, err)
	}
	if got[0].Reactions != nil {
		t.Fatal("reactions persisted")
	}
	m, err := s.Message(2, 9)
	if err != nil || m == nil || m.ChannelID != 2 {
		t.Fatalf("message=%v err=%v", m, err)
	}
}
func TestStoreConcurrentAndPages(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var wg sync.WaitGroup
	for i := 1; i <= 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if e := s.Put([]discord.Message{msg(1, discord.MessageID(i))}); e != nil {
				t.Error(e)
			}
		}(i)
	}
	wg.Wait()
	if err = s.SavePage(1, 0, 1, []discord.Message{msg(1, 20)}); err != nil {
		t.Fatal(err)
	}
	got, complete, err := s.CachedPage(1, 0, 50)
	if err != nil || len(got) != 1 || complete {
		t.Fatalf("got=%d complete=%v err=%v", len(got), complete, err)
	}
	if err = s.SavePage(1, 10, 2, []discord.Message{msg(1, 9), msg(1, 8)}); err != nil {
		t.Fatal(err)
	}
	got, _, err = s.CachedPage(1, 10, 50)
	if err != nil || len(got) != 2 {
		t.Fatalf("older page=%v err=%v", got, err)
	}
	if err = s.Delete(1, []discord.MessageID{20}); err != nil {
		t.Fatal(err)
	}
	got, _, err = s.CachedPage(1, 0, 50)
	if err != nil || len(got) != 0 {
		t.Fatalf("deleted page=%v err=%v", got, err)
	}
}
func TestInvalidateLatest(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.SavePage(1, 0, 1, []discord.Message{msg(1, 4)}); err != nil {
		t.Fatal(err)
	}
	if err = s.InvalidateLatest(1); err != nil {
		t.Fatal(err)
	}
	m, _, err := s.CachedPage(1, 0, 5)
	if err != nil || m != nil {
		t.Fatalf("cached=%v err=%v", m, err)
	}
}

func TestStoreSurvivesAbruptExit(t *testing.T) {
	path := os.Getenv("DISCORDO_TEST_STORE")
	if path != "" {
		store, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SavePage(1, 0, 2, historyRows(3, 2)); err != nil {
			t.Fatal(err)
		}
		// Exit without Close, as when the process is terminated unexpectedly.
		os.Exit(0)
	}
	path = filepath.Join(t.TempDir(), "abrupt.db")
	command := exec.Command(os.Args[0], "-test.run=^TestStoreSurvivesAbruptExit$")
	command.Env = append(os.Environ(), "DISCORDO_TEST_STORE="+path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("child process: %v\n%s", err, output)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	rows, complete, err := store.CachedPage(1, 0, 2)
	requireIDs(t, rows, err, 3, 2)
	if !complete {
		t.Fatal("committed page coverage was lost")
	}
}

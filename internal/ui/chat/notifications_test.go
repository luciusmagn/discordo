package chat

import (
	"testing"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
)

func TestActiveMute(t *testing.T) {
	future := discord.Timestamp(time.Now().Add(time.Hour))
	past := discord.Timestamp(time.Now().Add(-time.Hour))
	for _, tt := range []struct {
		name   string
		config *gateway.UserMuteConfig
		muted  bool
		want   bool
	}{
		{"off", nil, false, false},
		{"permanent", nil, true, true},
		{"sentinel permanent", &gateway.UserMuteConfig{SelectedTimeWindow: -1}, true, true},
		{"active expiry", &gateway.UserMuteConfig{SelectedTimeWindow: 1, EndTime: future}, true, true},
		{"expired", &gateway.UserMuteConfig{SelectedTimeWindow: 1, EndTime: past}, true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := activeMute(tt.config, tt.muted); got != tt.want {
				t.Fatalf("activeMute() = %v, want %v", got, tt.want)
			}
		})
	}
}

const (
	testGuildID   discord.GuildID   = 10
	testChannelID discord.ChannelID = 20
	testUserID    discord.UserID    = 30
	testOtherID   discord.UserID    = 40
	testRoleID    discord.RoleID    = 50
)

func notificationTestModel(t *testing.T) *Model {
	t.Helper()
	m := newTestModel(t)
	m.cfg.Notifications.Enabled = true
	m.cfg.Status = discord.OnlineStatus
	if err := m.state.Cabinet.MyselfSet(discord.User{ID: testUserID, Username: "me"}, false); err != nil {
		t.Fatal(err)
	}
	if err := m.state.Cabinet.ChannelSet(&discord.Channel{ID: testChannelID, GuildID: testGuildID, Type: discord.GuildText}, false); err != nil {
		t.Fatal(err)
	}
	if err := m.state.Cabinet.GuildSet(&discord.Guild{ID: testGuildID, Name: "guild"}, false); err != nil {
		t.Fatal(err)
	}
	if err := m.state.MemberStore.MemberSet(testGuildID, &discord.Member{User: discord.User{ID: testUserID}, RoleIDs: []discord.RoleID{testRoleID}}, false); err != nil {
		t.Fatal(err)
	}
	return m
}

func notificationMessage(mentions ...discord.UserID) *discord.Message {
	users := make([]discord.GuildUser, len(mentions))
	for i, id := range mentions {
		users[i] = discord.GuildUser{User: discord.User{ID: id}}
	}
	return &discord.Message{GuildID: testGuildID, ChannelID: testChannelID, Author: discord.User{ID: testOtherID}, Mentions: users, Content: "message"}
}

func applyGuildSettings(m *Model, setting gateway.UserGuildSetting) {
	m.state.State.Handler.Call(&gateway.UserGuildSettingsUpdateEvent{UserGuildSetting: setting})
}

func TestShouldNotifyUsesLiveGuildSettings(t *testing.T) {
	for _, tt := range []struct {
		name    string
		setting gateway.UserGuildSetting
		message *discord.Message
		want    bool
	}{
		{"only mentions ordinary", gateway.UserGuildSetting{GuildID: testGuildID, Notifications: gateway.OnlyMentions}, notificationMessage(), false},
		{"only mentions direct mention", gateway.UserGuildSetting{GuildID: testGuildID, Notifications: gateway.OnlyMentions}, notificationMessage(testUserID), true},
		{"channel all overrides guild only mentions", gateway.UserGuildSetting{GuildID: testGuildID, Notifications: gateway.OnlyMentions, ChannelOverrides: []gateway.UserChannelOverride{{ChannelID: testChannelID, Notifications: gateway.AllNotifications}}}, notificationMessage(), true},
		{"explicit none suppresses direct mention", gateway.UserGuildSetting{GuildID: testGuildID, Notifications: gateway.AllNotifications, ChannelOverrides: []gateway.UserChannelOverride{{ChannelID: testChannelID, Notifications: gateway.NoNotifications}}}, notificationMessage(testUserID), false},
		{"muted channel suppresses ordinary message", gateway.UserGuildSetting{GuildID: testGuildID, Notifications: gateway.AllNotifications, ChannelOverrides: []gateway.UserChannelOverride{{ChannelID: testChannelID, Notifications: gateway.AllNotifications, Muted: true}}}, notificationMessage(), false},
		{"suppressed role mention", gateway.UserGuildSetting{GuildID: testGuildID, Notifications: gateway.OnlyMentions, SuppressRoles: true}, &discord.Message{GuildID: testGuildID, ChannelID: testChannelID, Author: discord.User{ID: testOtherID}, MentionRoleIDs: []discord.RoleID{testRoleID}, Content: "role"}, false},
		{"suppressed everyone mention", gateway.UserGuildSetting{GuildID: testGuildID, Notifications: gateway.OnlyMentions, SuppressEveryone: true}, &discord.Message{GuildID: testGuildID, ChannelID: testChannelID, Author: discord.User{ID: testOtherID}, MentionEveryone: true, Content: "everyone"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := notificationTestModel(t)
			applyGuildSettings(m, tt.setting)
			if got := m.shouldNotify(tt.message); got != tt.want {
				t.Fatalf("shouldNotify() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestShouldNotifySuppressesOwnBlockedAndDND(t *testing.T) {
	for _, tt := range []struct {
		name  string
		setup func(*Model)
		msg   *discord.Message
	}{
		{"own sender", nil, &discord.Message{ChannelID: testChannelID, Author: discord.User{ID: testUserID}, Content: "own"}},
		{"blocked sender", func(m *Model) {
			m.state.State.Handler.Call(&gateway.RelationshipAddEvent{Relationship: discord.Relationship{UserID: testOtherID, Type: discord.BlockedRelationship}})
		}, notificationMessage()},
		{"configured dnd", func(m *Model) { m.cfg.Status = discord.DoNotDisturbStatus }, notificationMessage()},
		{"live dnd", func(m *Model) {
			if err := m.state.PresenceStore.PresenceSet(0, &discord.Presence{User: discord.User{ID: testUserID}, Status: discord.DoNotDisturbStatus}, false); err != nil {
				t.Fatal(err)
			}
		}, notificationMessage()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := notificationTestModel(t)
			if tt.setup != nil {
				tt.setup(m)
			}
			if got := m.shouldNotify(tt.msg); got {
				t.Fatal("shouldNotify() = true, want false")
			}
		})
	}
}

func TestShouldNotifyDM(t *testing.T) {
	m := notificationTestModel(t)
	if err := m.state.Cabinet.ChannelSet(&discord.Channel{ID: testChannelID, Type: discord.DirectMessage, DMRecipients: []discord.User{{ID: testOtherID}}}, false); err != nil {
		t.Fatal(err)
	}
	if got := m.shouldNotify(notificationMessage()); !got {
		t.Fatal("shouldNotify() = false, want true for a direct message")
	}
}

func TestNotificationSettingsChangesAndRoleMentions(t *testing.T) {
	m := notificationTestModel(t)
	setting := gateway.UserGuildSetting{GuildID: testGuildID, Notifications: gateway.OnlyMentions}
	applyGuildSettings(m, setting)
	if m.shouldNotify(notificationMessage()) {
		t.Fatal("ordinary message notified under mentions-only policy")
	}
	role := notificationMessage()
	role.MentionRoleIDs = []discord.RoleID{testRoleID}
	role.MentionEveryone = true
	setting.SuppressEveryone = true
	applyGuildSettings(m, setting)
	if !m.shouldNotify(role) {
		t.Fatal("suppressed everyone mention hid a valid role mention")
	}
	setting.SuppressRoles = true
	applyGuildSettings(m, setting)
	if m.shouldNotify(role) {
		t.Fatal("suppressed role mention notified")
	}
	setting.Notifications = gateway.AllNotifications
	applyGuildSettings(m, setting)
	if !m.shouldNotify(notificationMessage()) {
		t.Fatal("synced all-messages setting was not applied")
	}
}

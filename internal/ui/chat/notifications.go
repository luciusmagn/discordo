package chat

import (
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
)

// shouldNotify applies the local copy of Discord's message notification policy.
// It is deliberately synchronous: notification decisions must not issue requests.
func (m Model) shouldNotify(message *discord.Message) bool {
	if !m.cfg.Notifications.Enabled || m.cfg.Status == discord.DoNotDisturbStatus || m.state.Status() == discord.DoNotDisturbStatus {
		return false
	}
	me, _ := m.state.Cabinet.Me()
	if me == nil || message.Author.ID == me.ID || m.state.UserIsBlocked(message.Author.ID) {
		return false
	}

	guild := m.state.MutedState.GuildSettings(message.GuildID)
	mention := false
	for _, user := range message.Mentions {
		if user.ID == me.ID {
			mention = true
			break
		}
	}
	if !mention && message.GuildID.IsValid() && !guild.SuppressRoles {
		if member, err := m.state.Cabinet.Member(message.GuildID, me.ID); err == nil && member != nil {
			for _, role := range message.MentionRoleIDs {
				for _, owned := range member.RoleIDs {
					if role == owned {
						mention = true
						break
					}
				}
				if mention {
					break
				}
			}
		}
	}

	if message.GuildID.IsValid() && message.MentionEveryone && !guild.SuppressEveryone {
		mention = true
	}
	channel, err := m.state.Cabinet.Channel(message.ChannelID)
	if err != nil {
		return false
	}
	if channel.Type == discord.DirectMessage || channel.Type == discord.GroupDM {
		override := m.state.MutedState.ChannelOverrides(message.ChannelID)
		return override.Notifications != gateway.NoNotifications && !activeMute(override.MuteConfig, override.Muted)
	}

	override := m.state.MutedState.ChannelOverrides(message.ChannelID)
	level := override.Notifications
	if level == gateway.GuildDefaults {
		level = guild.Notifications
	}
	if level == gateway.NoNotifications {
		return false
	}
	if activeMute(override.MuteConfig, override.Muted) || m.state.MutedState.Category(message.ChannelID) || activeMute(guild.MuteConfig, guild.Muted) {
		return mention
	}
	switch level {
	case gateway.AllNotifications:
		return true
	case gateway.OnlyMentions:
		return mention
	default:
		return mention
	}
}

func activeMute(config *gateway.UserMuteConfig, muted bool) bool {
	if !muted {
		return false
	}
	if config == nil || config.SelectedTimeWindow == -1 {
		return true
	}
	return !config.EndTime.Time().Before(time.Now())
}

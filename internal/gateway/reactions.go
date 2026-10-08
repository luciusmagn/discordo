package gateway

import (
	discordgateway "github.com/ayn2op/arikawa/v3/gateway"
	"github.com/ayn2op/arikawa/v3/utils/ws"
)

// ignoredReaction preserves the dispatch sequence without decoding reaction data.
// The state handlers cannot mistake it for a message-reaction event.
type ignoredReaction struct {
	kind ws.EventType
}

func (e *ignoredReaction) Op() ws.OpCode              { return 0 }
func (e *ignoredReaction) EventType() ws.EventType    { return e.kind }
func (e *ignoredReaction) UnmarshalJSON([]byte) error { return nil }

func messageUnmarshalers(capabilities discordgateway.Capabilities) ws.OpUnmarshalers {
	unmarshalers := discordgateway.NewOpUnmarshalers(capabilities)
	for _, kind := range []ws.EventType{
		"MESSAGE_REACTION_ADD",
		"MESSAGE_REACTION_ADD_MANY",
		"MESSAGE_REACTION_REMOVE",
		"MESSAGE_REACTION_REMOVE_ALL",
		"MESSAGE_REACTION_REMOVE_EMOJI",
	} {
		unmarshalers.Add(func() ws.Event { return &ignoredReaction{kind: kind} })
	}
	return unmarshalers
}

// IsIgnored reports events excluded from the application and its message cache.
func IsIgnored(event discordgateway.Event) bool {
	_, ignored := event.(*ignoredReaction)
	return ignored
}

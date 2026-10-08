package gateway

import (
	"context"
	"strings"
	"testing"

	discordgateway "github.com/ayn2op/arikawa/v3/gateway"
	"github.com/ayn2op/arikawa/v3/utils/ws"
)

func TestReactionDispatchPreservesSequence(t *testing.T) {
	codec := ws.NewCodec(messageUnmarshalers(discordgateway.DebounceMessageReactions))
	for _, kind := range []string{"MESSAGE_REACTION_ADD", "MESSAGE_REACTION_ADD_MANY", "MESSAGE_REACTION_REMOVE", "MESSAGE_REACTION_REMOVE_ALL", "MESSAGE_REACTION_REMOVE_EMOJI"} {
		t.Run(kind, func(t *testing.T) {
			out := make(chan ws.Op, 1)
			payload := `{"op":0,"s":42,"t":"` + kind + `","d":{"message_id":"123","channel_id":"456"}}`
			buffer := ws.NewDecodeBuffer(1024)
			if err := codec.DecodeInto(context.Background(), strings.NewReader(payload), &buffer, out); err != nil {
				t.Fatal(err)
			}
			op := <-out
			if !IsIgnored(op.Data) || op.Sequence != 42 || op.Type != ws.EventType(kind) {
				t.Fatalf("dispatch = %#v", op)
			}
		})
	}
	if _, ok := messageUnmarshalers(0).Lookup(0, "MESSAGE_CREATE")().(*discordgateway.MessageCreateEvent); !ok {
		t.Fatal("message creation decoder was replaced")
	}
}

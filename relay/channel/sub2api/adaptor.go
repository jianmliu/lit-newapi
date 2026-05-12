package sub2api

import (
	"github.com/QuantumNous/new-api/relay/channel/openai"
)

// Adaptor is the relay channel adaptor for the Sub2API marketplace runtime.
// Sub2API endpoints accept OpenAI-compatible chat-completion bodies, so we
// inherit the entire OpenAI adaptor (request body conversion, response
// parsing, streaming) and only need to differentiate via the channel name.
// URL rewrite to /sub2api/v1/endpoints/{endpoint_id}/chat/completions and
// the runtime-key bearer injection / X-Sub2API-* header stripping live in
// a separate follow-up commit together with the channel-config wiring that
// supplies the endpoint_id and runtime-key env-var name at request time.
type Adaptor struct {
	openai.Adaptor
}

func (a *Adaptor) GetChannelName() string {
	return "Sub2API"
}

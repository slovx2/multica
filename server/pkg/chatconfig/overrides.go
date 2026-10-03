// Package chatconfig defines session-only execution overrides. Empty values
// inherit the agent configuration; "default" is Codex's explicit Standard tier.
package chatconfig

import "encoding/json"

type Overrides struct {
	ThinkingLevel string `json:"thinking_level,omitempty"`
	ServiceTier   string `json:"service_tier,omitempty"`
}

func (o Overrides) Empty() bool { return o.ThinkingLevel == "" && o.ServiceTier == "" }

func Decode(raw []byte) Overrides {
	var o Overrides
	_ = json.Unmarshal(raw, &o)
	return o
}

func (o Overrides) JSON() []byte {
	raw, _ := json.Marshal(o)
	return raw
}

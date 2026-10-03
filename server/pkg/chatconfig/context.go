package chatconfig

import "encoding/json"

func Action(raw []byte) string {
	var v struct {
		Action string `json:"chat_action"`
	}
	_ = json.Unmarshal(raw, &v)
	return v.Action
}

package a2asrv

import "github.com/a2aproject/a2a-go/v2/a2a"

const (
	extensionURI = "https://a2a.alis.build/extensions/scheduler/v1"
)

var AgentExtension a2a.AgentExtension = a2a.AgentExtension{
	Description: "Enables agents to schedule and manage CRON tasks",
	Params:      map[string]any{},
	Required:    false,
	URI:         extensionURI,
}
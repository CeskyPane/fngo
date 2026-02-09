package tasruntime

import (
	"context"

	"github.com/ceskypane/fngo"
)

const (
	OpTaskRun   = "task.run"
	OpTaskStop  = "task.stop"
	OpTaskReset = "task.reset"
	OpBotPing   = "bot.ping"
	OpBotLogout = "bot.logout"

	OpCmdAck        = "cmd.ack"
	OpCmdErr        = "cmd.err"
	OpBotPong       = "bot.pong"
	OpBotHeartbeat  = "bot.heartbeat"
	OpBotStopped    = "bot.stopped"
	OpAttemptResult = "attempt.result"
)

type Envelope struct {
	CID  string         `json:"cid"`
	Op   string         `json:"op"`
	TS   string         `json:"ts"`
	Data map[string]any `json:"data,omitempty"`
}

type TaskRunPayload struct {
	Task struct {
		ID             int      `json:"id"`
		ExecutionID    string   `json:"execution_id"`
		LobbyID        string   `json:"lobby_id"`
		SubMatchID     string   `json:"sub_match_id"`
		OrganizationID string   `json:"organization_id"`
		Players        []string `json:"players"`
		CaptainID      string   `json:"captain_id"`
		Playlist       string   `json:"playlist"`
		CustomKey      string   `json:"custom_key"`
		Attempt        int      `json:"attempt"`
	} `json:"task"`
	Attempt struct {
		ID        int    `json:"id"`
		Number    int    `json:"number"`
		BotID     int    `json:"bot_id"`
		StartedAt string `json:"started_at"`
	} `json:"attempt"`
}

type AttemptResultPayload struct {
	TaskID      int            `json:"task_id"`
	AttemptID   int            `json:"attempt_id"`
	ExecutionID string         `json:"execution_id"`
	Result      string         `json:"result"`
	FlowState   string         `json:"flow_state"`
	FlowError   string         `json:"flow_error,omitempty"`
	Extra       map[string]any `json:"-"`
}

// BotRuntimeAdapter is a thin TAS-facing layer. It is responsible for protocol mapping,
// while Fortnite protocol behavior remains inside fngo.BotClient implementations.
type BotRuntimeAdapter interface {
	HandleCommand(ctx context.Context, botName string, cmd Envelope) ([]Envelope, error)
}

// ClientFactory resolves a bot-specific fngo client for runtime command handling.
type ClientFactory interface {
	ClientForBot(ctx context.Context, botName string) (fngo.BotClient, error)
}

// TaskFlowExecutor executes TAS task.run payloads using a resolved fngo.BotClient.
type TaskFlowExecutor interface {
	RunTask(ctx context.Context, client fngo.BotClient, payload TaskRunPayload) (AttemptResultPayload, error)
	StopTask(ctx context.Context, botName string, reason string) error
}

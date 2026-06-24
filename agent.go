package ava

import (
	"context"
	_ "embed"
	"fmt"

	apisess "go.naturallyfunny.dev/api/session"
	apitime "go.naturallyfunny.dev/api/time"
	apiuser "go.naturallyfunny.dev/api/user"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	adktool "google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

//go:embed internal/system-instruction.txt
var systemInstruction string

// SubAgent is a specialist Ava can delegate to. Ava knows only how to describe
// it to the model (Name/Description) and how to hand it a message (Message) —
// not how Message is fulfilled. The consumer (chat) implements Message by
// running the specialist's own in-process runner on the shared session, so the
// specialist persists its own turn (authored as itself), reads the full shared
// history, and decides independently how — or whether — to reply. This is the
// in-process successor to the old HTTP AgentClient: same contract, no network.
type SubAgent interface {
	Name() string
	Description() string
	Run(ctx context.Context, message string) (string, error)
}

// Config holds the dependencies a consumer must supply. Ava's identity and base
// system instruction are owned by the module. Per-channel instruction (text,
// voice, recall) is the consumer's concern, applied on the runner it owns.
type Config struct {
	Model model.LLM
	// SubAgents are the specialists Ava can delegate to. Each is wired as an ADK
	// tool whose declaration is the specialist's Name/Description, so the model
	// chooses delegation the same way it chooses any tool.
	SubAgents []SubAgent
}

// New builds the Ava agent — the Avagenc orchestrator. It returns a bare agent;
// running it (runner, session, per-channel instruction) is the consumer's job.
func New(cfg Config) (agent.Agent, error) {
	if cfg.Model == nil {
		return nil, fmt.Errorf("ava: model is required")
	}

	tools := make([]adktool.Tool, 0, len(cfg.SubAgents))
	for _, subAgent := range cfg.SubAgents {
		t, err := subAgentToADKTool(subAgent)
		if err != nil {
			return nil, err
		}
		tools = append(tools, t)
	}

	instruction := "[SYSTEM_INSTRUCTION]" + systemInstruction + "\n[/SYSTEM_INSTRUCTION]"

	ava, err := llmagent.New(llmagent.Config{
		Name:        "ava",
		Description: "Avagenc Orchestrator Agent",
		Model:       cfg.Model,
		Instruction: instruction,
		Tools:       tools,
	})
	if err != nil {
		return nil, fmt.Errorf("ava: agent: %w", err)
	}
	return ava, nil
}

type subAgentToolArg struct {
	// Message is the instruction for the specialist. It may be empty:
	// Ava knows that they are in chat session and specialist reads the shared
	// session history and acts on it, so Ava need not restate context already visible there.
	Message string `json:"message"`
}

type subAgentToolOutput struct {
	Response string `json:"response"`
}


// toolCtxToCtx carries the human's identity, the shared session id, and the
// timezone from Ava's tool-call context into the specialist's run, so the
// specialist addresses the same Zep thread and localizes time identically.
func toolCtxToCtx(toolCtx agent.ToolContext) (context.Context, error) {
	userID := toolCtx.UserID()
	if userID == "" {
		return nil, fmt.Errorf("ava: delegation: missing user identity")
	}
	ctx, err := apiuser.ContextWithID(toolCtx, userID)
	if err != nil {
		return nil, fmt.Errorf("ava: delegation: attach user: %w", err)
	}
	if sessionID := toolCtx.SessionID(); sessionID != "" {
		ctx, err = apisess.ContextWithID(ctx, sessionID)
		if err != nil {
			return nil, fmt.Errorf("ava: delegation: attach session: %w", err)
		}
	}
	if tz, ok := toolCtx.Value(apitime.ContextKey).(string); ok && tz != "" {
		ctx, _ = apitime.ContextWithZone(ctx, tz)
	}
	return ctx, nil
}

func subAgentToADKTool(subAgent SubAgent) (adktool.Tool, error) {
	t, err := functiontool.New(
		functiontool.Config{
			Name:        subAgent.Name(),
			Description: subAgent.Description(),
		},
		func(toolCtx agent.ToolContext, in subAgentToolArg) (subAgentToolOutput, error) {
			ctx, err := toolCtxToCtx(toolCtx)
			if err != nil {
				return subAgentToolOutput{}, err
			}
			reply, err := subAgent.Run(ctx, in.Message)
			if err != nil {
				return subAgentToolOutput{}, fmt.Errorf("ava: delegate to %s: %w", subAgent.Name(), err)
			}
			return subAgentToolOutput{Response: reply}, nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("ava: build delegation tool %q: %w", subAgent.Name(), err)
	}
	return t, nil
}

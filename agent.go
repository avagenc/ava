package ava

import (
	"context"
	_ "embed"
	"fmt"

	posteraadk "go.naturallyfunny.dev/agentkit/postera/adk"
	apisess "go.naturallyfunny.dev/api/session"
	apitime "go.naturallyfunny.dev/api/time"
	apiuser "go.naturallyfunny.dev/api/user"
	"go.naturallyfunny.dev/postera"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	adktool "google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

//go:embed internal/instruction.txt
var systemInstruction string

type SubAgent interface {
	Name() string
	Description() string
	Run(ctx context.Context, message string) (string, error)
}

type Config struct {
	Model                 model.LLM
	Postarius             *postera.Postarius
	SubAgents             []SubAgent
	AdditionalInstruction string
}

func New(cfg Config) (agent.Agent, error) {
	if cfg.Model == nil {
		return nil, fmt.Errorf("ava: model is required")
	}
	if cfg.Postarius == nil {
		return nil, fmt.Errorf("ava: postarius is required")
	}

	posteraTools, err := posteraadk.Tools(cfg.Postarius)
	if err != nil {
		return nil, fmt.Errorf("ava: postera tools: %w", err)
	}

	tools := make([]adktool.Tool, 0, len(cfg.SubAgents)+len(posteraTools))
	tools = append(tools, posteraTools...)
	for _, subAgent := range cfg.SubAgents {
		t, err := subAgentToADKTool(subAgent)
		if err != nil {
			return nil, err
		}
		tools = append(tools, t)
	}

	instruction := "[SYSTEM_INSTRUCTION]" + systemInstruction + "\n[/SYSTEM_INSTRUCTION]"
	if cfg.AdditionalInstruction != "" {
		instruction = "[SYSTEM_INSTRUCTION]" + systemInstruction + "\n\n" + cfg.AdditionalInstruction + "\n[/SYSTEM_INSTRUCTION]"
	}

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
	Message string `json:"message"`
}

type subAgentToolOutput struct {
	Response string `json:"response"`
}

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

package llm

import "context"

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

type Message struct {
	Role    Role
	Content string
}

type Request struct {
	Model    string
	Messages []Message
}

type Provider interface {
	ListModels(ctx context.Context) ([]string, error)
	Chat(ctx context.Context, req Request, onDelta func(string)) (Message, error)
}

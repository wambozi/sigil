package inference

// OpenAI-compatible request/response types used by both local and cloud backends.

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model     string        `json:"model"`
	Messages  []chatMessage `json:"messages"`
	MaxTokens int           `json:"max_tokens,omitempty"`
}

type chatChoice struct {
	Message chatMessage `json:"message"`
}

type chatResponse struct {
	Choices []chatChoice `json:"choices"`
}

// chatResponseWithTools is the response shape when tool_calls may be present.
type chatToolChoice struct {
	Message struct {
		Role      string         `json:"role"`
		Content   string         `json:"content"`
		ToolCalls []ChatToolCall `json:"tool_calls,omitempty"`
	} `json:"message"`
}

type chatResponseWithTools struct {
	Choices []chatToolChoice `json:"choices"`
}

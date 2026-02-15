package dash

import "context"

// SummaryClient generates text summaries and completions from content.
type SummaryClient interface {
	Summarize(ctx context.Context, content, filePath string) (string, error)
	Complete(ctx context.Context, systemPrompt, userMsg string) (string, error)
}

// NoOpSummarizer is a no-op summarizer (used when no API key is configured).
type NoOpSummarizer struct{}

// Summarize returns empty string for NoOpSummarizer.
func (n *NoOpSummarizer) Summarize(ctx context.Context, content, filePath string) (string, error) {
	return "", nil
}

// Complete returns empty string for NoOpSummarizer.
func (n *NoOpSummarizer) Complete(ctx context.Context, systemPrompt, userMsg string) (string, error) {
	return "", nil
}

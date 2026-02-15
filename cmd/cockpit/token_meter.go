package main

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

type tokenMeter struct {
	totalUsed int // total tokens used (prompt + completion)
	prompt     int // prompt tokens from latest API call (= full context sent)
	completion int // completion tokens from latest API call
	limit      int
	exchanges  int // number of user→assistant exchanges
}

func newTokenMeter(limit int) tokenMeter {
	return tokenMeter{limit: limit}
}

// set updates with real API usage from the latest response.
// prompt_tokens already includes the full conversation sent.
func (tm *tokenMeter) set(prompt, completion int) {
	tm.prompt = prompt
	tm.completion = completion
	tm.totalUsed = prompt + completion
}

func (tm *tokenMeter) addExchange() {
	tm.exchanges++
}

func (tm *tokenMeter) total() int {
	return tm.prompt + tm.completion
}

// usedTokens returns the total tokens used.
func (tm *tokenMeter) usedTokens() int {
	return tm.totalUsed
}

func (tm *tokenMeter) pct() int {
	if tm.limit == 0 {
		return 0
	}
	p := tm.total() * 100 / tm.limit
	if p > 100 {
		p = 100
	}
	return p
}

func (tm *tokenMeter) shouldHandoff(maxExchanges int) bool {
	return tm.exchanges >= maxExchanges
}

func (tm *tokenMeter) View() string {
	t := tm.total()
	if t == 0 {
		return ""
	}

	p := tm.pct()
	barWidth := 12
	filled := p * barWidth / 100
	if filled > barWidth {
		filled = barWidth
	}
	empty := barWidth - filled

	var barStyle lipgloss.Style
	switch {
	case p >= 80:
		barStyle = lipgloss.NewStyle().Foreground(cAlert)
	case p >= 60:
		barStyle = lipgloss.NewStyle().Foreground(cWarning)
	default:
		barStyle = lipgloss.NewStyle().Foreground(cSuccess)
	}

	bar := barStyle.Render(repeat('\u2588', filled)) +
		lipgloss.NewStyle().Foreground(cDim).Render(repeat('\u2591', empty))

	usage := formatTokenCount(t) + "/" + formatTokenCount(tm.limit)
	return fmt.Sprintf("[%s] %s #%d", bar, usage, tm.exchanges)
}

func formatTokenCount(n int) string {
	if n >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%dk", n/1000)
	}
	return fmt.Sprintf("%d", n)
}

func repeat(r rune, n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]rune, n)
	for i := range b {
		b[i] = r
	}
	return string(b)
}

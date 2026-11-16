package monitor

import (
	"fmt"
	"io"
	"sync"

	"github.com/impiperl/Tweedle/internal/i18n"
)

// LogNotifier writes every event as one line (stdout → journald).
type LogNotifier struct {
	Lang string
	Out  io.Writer
	mu   sync.Mutex
}

func (l *LogNotifier) Notify(e Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.Out, "%s [%s] %s: %s\n", e.Time.Format("2006-01-02 15:04:05"),
		i18n.T(l.Lang, "level_"+string(e.Level), nil), e.Server, i18n.T(l.Lang, e.Code, e.Params))
}

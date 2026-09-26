package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/abevz/dibs/internal/client"
	"github.com/abevz/dibs/internal/watch"
)

func runWatch(ctx context.Context, c *client.Client, args []string) error {
	var project string
	once := jsonOutput
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--project":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return argumentError("--project requires a key")
			}
			project = args[i+1]
			i++
		case "--once":
			once = true
		default:
			return argumentError("unknown watch flag: " + args[i])
		}
	}
	svc := watch.New(c, project)
	if once {
		snapshot, err := svc.Refresh(ctx, time.Now())
		if err != nil {
			return err
		}
		if jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(snapshot)
		}
		fmt.Fprintln(os.Stdout, watch.Render(snapshot, nil, time.Now(), 100, 28))
		return nil
	}
	if !terminalDevice(os.Stdin) || !terminalDevice(os.Stdout) {
		return argumentError("watch requires a terminal; use --once or --json for one snapshot")
	}
	model := watchModel{
		ctx:      ctx,
		service:  svc,
		snapshot: watch.Snapshot{Project: project},
		width:    100,
		height:   28,
		loading:  true,
		now:      time.Now(),
	}
	_, err := tea.NewProgram(model, tea.WithAltScreen(), tea.WithContext(ctx)).Run()
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		return nil
	}
	return err
}

func terminalDevice(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

type watchSnapshotMsg struct {
	snapshot watch.Snapshot
	err      error
}

type watchRefreshMsg struct{}
type watchClockMsg time.Time

type watchModel struct {
	ctx      context.Context
	service  *watch.Service
	snapshot watch.Snapshot
	lastErr  error
	width    int
	height   int
	loading  bool
	now      time.Time
}

func (m watchModel) Init() tea.Cmd {
	return tea.Batch(m.fetch(), watchClock())
}

func (m watchModel) fetch() tea.Cmd {
	return func() tea.Msg {
		snapshot, err := m.service.Refresh(m.ctx, time.Now())
		return watchSnapshotMsg{snapshot: snapshot, err: err}
	}
}

func watchClock() tea.Cmd {
	return tea.Tick(time.Second, func(now time.Time) tea.Msg { return watchClockMsg(now) })
}

func watchRefresh() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return watchRefreshMsg{} })
}

func (m watchModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "r":
			if !m.loading {
				m.loading = true
				return m, m.fetch()
			}
		}
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case watchClockMsg:
		m.now = time.Time(msg)
		return m, watchClock()
	case watchRefreshMsg:
		if !m.loading {
			m.loading = true
			return m, m.fetch()
		}
	case watchSnapshotMsg:
		m.loading = false
		m.lastErr = msg.err
		if msg.err == nil {
			m.snapshot = msg.snapshot
		}
		return m, watchRefresh()
	}
	return m, nil
}

func (m watchModel) View() string {
	return watch.Render(m.snapshot, m.lastErr, m.now, m.width, m.height)
}

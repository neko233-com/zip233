package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"

	"github.com/neko233-com/zip233/internal/archive"
	"github.com/neko233-com/zip233/internal/contract"
	"github.com/neko233-com/zip233/internal/version"
)

// tuiActive tells main that the outcome (including errors) was already drawn
// by the TUI, so main must only set the exit code and not print twice.
var tuiActive bool

const cardWidth = 56

var (
	headingStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("255"))
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	keyStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	valueStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("255"))
	statusStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	okStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	badStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("196"))
	dividerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	cardBase     = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 1).
		Width(cardWidth)
)

// useTUI decides whether the terminal can host an interactive program. Piped
// stdin/stdout and --json always keep the classic, script-friendly output.
func useTUI() bool {
	if jsonOutput {
		return false
	}
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

type progressMsg struct{ p archive.Progress }

type finishedMsg struct {
	title   string
	lines   []string
	failed  bool
	err     error
	elapsed time.Duration
}

type autoQuitMsg struct{}

type tuiOutcome struct {
	lines  []string
	failed bool
	err    error
}

type tuiJob struct {
	command      string
	target       string
	withProgress bool
	work         func() tuiOutcome
}

type tuiModel struct {
	command      string
	target       string
	withProgress bool

	spinner spinner.Model
	bar     progress.Model

	started  time.Time
	elapsed  time.Duration
	status   string
	phase    string
	pgDone   int64
	pgTotal  int64
	finished bool
	failed   bool
	title    string
	lines    []string
	err      error
}

func (m tuiModel) Init() tea.Cmd { return m.spinner.Tick }

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		}
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case progressMsg:
		m.phase = msg.p.Phase
		m.pgDone = msg.p.Done
		m.pgTotal = msg.p.Total
		if msg.p.Message != "" {
			m.status = msg.p.Message
		}
		return m, nil
	case finishedMsg:
		m.finished = true
		m.failed = msg.failed || msg.err != nil
		m.title = msg.title
		m.lines = msg.lines
		m.err = msg.err
		m.elapsed = msg.elapsed
		return m, tea.Tick(5*time.Second, func(time.Time) tea.Msg { return autoQuitMsg{} })
	case autoQuitMsg:
		return m, tea.Quit
	}
	return m, nil
}

func (m tuiModel) View() string {
	var b strings.Builder

	b.WriteString(headingStyle.Render("▲ zip233"))
	b.WriteString("  ")
	b.WriteString(dimStyle.Render("v" + version.Version))
	b.WriteString("\n")
	b.WriteString(dividerStyle.Render(strings.Repeat("─", cardWidth-2)))
	b.WriteString("\n\n")

	b.WriteString(tuiKV("命令", m.command))
	b.WriteString("\n")
	b.WriteString(tuiKV("目标", m.target))
	b.WriteString("\n\n")

	if !m.finished {
		b.WriteString(statusStyle.Render(m.spinner.View() + "  " + m.status))
		b.WriteString("\n")
		if m.withProgress {
			pct := float64(0)
			if m.pgTotal > 0 {
				pct = float64(m.pgDone) / float64(m.pgTotal)
			}
			if pct > 1 {
				pct = 1
			}
			if pct < 0 {
				pct = 0
			}
			b.WriteString(m.bar.ViewAs(pct))
			b.WriteString(fmt.Sprintf("  %3.0f%%  %d/%d", pct*100, m.pgDone, m.pgTotal))
			b.WriteString("\n")
		}
		b.WriteString(dimStyle.Render("耗时 " + time.Since(m.started).Round(time.Millisecond).String()))
		b.WriteString("\n")
	} else {
		title := okStyle.Render(m.title)
		if m.failed {
			title = badStyle.Render(m.title)
		}
		card := cardBase
		if m.failed {
			card = card.BorderForeground(lipgloss.Color("196"))
		}
		body := strings.Join(m.lines, "\n")
		inner := title + "  " + dimStyle.Render("耗时 "+m.elapsed.Round(time.Millisecond).String()) +
			"\n" + dividerStyle.Render(strings.Repeat("─", cardWidth-6)) + "\n" + body
		b.WriteString(card.Render(inner))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(dimStyle.Render("q / Esc / ctrl+c 退出"))
	return b.String()
}

// runTUI takes over the terminal while job.work runs in the background. The
// archive progress callback and the worker both inject messages with Send,
// which is safe to call from another goroutine.
func runTUI(job tuiJob) error {
	tuiActive = true
	started := time.Now()
	m := tuiModel{
		command:      job.command,
		target:       job.target,
		withProgress: job.withProgress,
		spinner:      spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		bar:          progress.New(progress.WithWidth(34), progress.WithSolidFill("255"), progress.WithoutPercentage()),
		started:      started,
		status:       "正在启动…",
	}
	p := tea.NewProgram(m, tea.WithAltScreen())

	archive.SetProgress(func(pg archive.Progress) {
		p.Send(progressMsg{p: pg})
	})
	go func() {
		out := job.work()
		archive.SetProgress(nil)
		title := "成功"
		if out.failed || out.err != nil {
			title = "失败"
		}
		p.Send(finishedMsg{
			title:   title,
			lines:   out.lines,
			failed:  out.failed,
			err:     out.err,
			elapsed: time.Since(started),
		})
	}()

	finalModel, err := p.Run()
	archive.SetProgress(nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "TUI 运行失败：%v\n", err)
		return err
	}
	if fm, ok := finalModel.(tuiModel); ok {
		if fm.err != nil {
			return fm.err
		}
		if fm.failed {
			return fmt.Errorf("操作失败")
		}
	}
	return nil
}

// archiveOutcome turns an archive result into the card payload shared by
// test/extract/create.
func archiveOutcome(result contract.ToolResult, err error) tuiOutcome {
	if err != nil {
		return tuiOutcome{lines: []string{err.Error()}, failed: true, err: err}
	}
	if !result.OK {
		return tuiOutcome{lines: []string{result.Stderr}, failed: true}
	}
	return tuiOutcome{lines: []string{result.Stdout}}
}

// runArchiveTUI is the TUI wrapper for operations that report progress.
func runArchiveTUI(command, target string, op func() (contract.ToolResult, error)) error {
	return runTUI(tuiJob{
		command:      command,
		target:       target,
		withProgress: true,
		work: func() tuiOutcome {
			result, err := op()
			return archiveOutcome(result, err)
		},
	})
}

// tuiKV renders a label/value pair for the quick-command result cards.
func tuiKV(key, value string) string {
	return keyStyle.Render(key) + "   " + valueStyle.Render(value)
}

package timer

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var taskIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

type Schedule struct {
	Kind     string   `json:"kind"`
	Every    string   `json:"every,omitempty"`
	At       string   `json:"at,omitempty"`
	Weekdays []string `json:"weekdays,omitempty"`
	Timezone string   `json:"timezone,omitempty"`
}

type Task struct {
	ID              string   `json:"id"`
	Enabled         bool     `json:"enabled"`
	Account         string   `json:"account"`
	ChatID          string   `json:"chat_id"`
	Prompt          string   `json:"prompt"`
	Cwd             string   `json:"cwd,omitempty"`
	AddDirs         []string `json:"add_dirs,omitempty"`
	Model           string   `json:"model,omitempty"`
	ReasoningEffort string   `json:"reasoning_effort,omitempty"`
	Schedule        Schedule `json:"schedule"`
	CreatedAt       string   `json:"created_at"`
	UpdatedAt       string   `json:"updated_at"`
	LastUpdatedBy   string   `json:"last_updated_by,omitempty"`
}

type State struct {
	ID               string `json:"id"`
	LastRunAt        string `json:"last_run_at,omitempty"`
	LastFinishedAt   string `json:"last_finished_at,omitempty"`
	LastSuccessAt    string `json:"last_success_at,omitempty"`
	LastError        string `json:"last_error,omitempty"`
	LastExitCode     int    `json:"last_exit_code,omitempty"`
	LastScheduledFor string `json:"last_scheduled_for,omitempty"`
	LastOutput       string `json:"last_output,omitempty"`
	NextRunAt        string `json:"next_run_at,omitempty"`
	UpdatedAt        string `json:"updated_at,omitempty"`
}

type Store struct {
	RepoRoot string
}

type Options struct {
	RepoRoot     string
	NodeBin      string
	PollInterval time.Duration
	Output       io.Writer
}

type Manager struct {
	opts    Options
	store   *Store
	mu      sync.Mutex
	running map[string]bool
}

func NewStore(repoRoot string) *Store {
	return &Store{RepoRoot: repoRoot}
}

func NewManager(opts Options) *Manager {
	if opts.PollInterval <= 0 {
		opts.PollInterval = 30 * time.Second
	}
	if opts.Output == nil {
		opts.Output = os.Stdout
	}
	return &Manager{
		opts:    opts,
		store:   NewStore(opts.RepoRoot),
		running: map[string]bool{},
	}
}

func (s *Store) timersDir() string {
	return filepath.Join(s.RepoRoot, "config", "timers")
}

func (s *Store) stateDir() string {
	return filepath.Join(s.RepoRoot, ".runtime", "timers", "state")
}

func (s *Store) logsDir() string {
	return filepath.Join(s.RepoRoot, ".runtime", "timers", "logs")
}

func (s *Store) TaskPath(id string) string {
	return filepath.Join(s.timersDir(), id+".json")
}

func (s *Store) StatePath(id string) string {
	return filepath.Join(s.stateDir(), id+".json")
}

func (s *Store) LogPath(id string) string {
	return filepath.Join(s.logsDir(), id+".log")
}

func (s *Store) ListTasks() ([]Task, error) {
	dir := s.timersDir()
	ents, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Task{}, nil
		}
		return nil, err
	}
	out := make([]Task, 0, len(ents))
	for _, ent := range ents {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		task, err := s.ReadTask(strings.TrimSuffix(ent.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		out = append(out, task)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *Store) ReadTask(id string) (Task, error) {
	var task Task
	b, err := os.ReadFile(s.TaskPath(id))
	if err != nil {
		return task, err
	}
	if err := json.Unmarshal(b, &task); err != nil {
		return task, err
	}
	if task.ID == "" {
		task.ID = id
	}
	return task, nil
}

func (s *Store) WriteTask(task Task) error {
	if err := ValidateTask(task); err != nil {
		return err
	}
	if err := os.MkdirAll(s.timersDir(), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.TaskPath(task.ID), append(body, '\n'), 0o644)
}

func (s *Store) DeleteTask(id string) error {
	if err := os.Remove(s.TaskPath(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Remove(s.StatePath(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Store) ReadState(id string) (State, error) {
	var st State
	b, err := os.ReadFile(s.StatePath(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{ID: id}, nil
		}
		return st, err
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return st, err
	}
	if st.ID == "" {
		st.ID = id
	}
	return st, nil
}

func (s *Store) WriteState(st State) error {
	if st.ID == "" {
		return fmt.Errorf("timer state id is required")
	}
	if err := os.MkdirAll(s.stateDir(), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.StatePath(st.ID), append(body, '\n'), 0o644)
}

func (s *Store) SetTaskEnabled(id string, enabled bool) (Task, error) {
	task, err := s.ReadTask(id)
	if err != nil {
		return Task{}, err
	}
	task.Enabled = enabled
	task.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := s.WriteTask(task); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (s *Store) ReadLogTail(id string, lines int) (string, error) {
	if lines <= 0 {
		lines = 40
	}
	file, err := os.Open(s.LogPath(id))
	if err != nil {
		return "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	buf := make([]string, 0, lines)
	for scanner.Scan() {
		buf = append(buf, scanner.Text())
		if len(buf) > lines {
			buf = buf[1:]
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return strings.TrimSpace(strings.Join(buf, "\n")), nil
}

func ValidateTask(task Task) error {
	if !taskIDPattern.MatchString(strings.TrimSpace(task.ID)) {
		return fmt.Errorf("invalid timer id %q", task.ID)
	}
	if strings.TrimSpace(task.Account) == "" {
		return fmt.Errorf("timer %s: account is required", task.ID)
	}
	if strings.TrimSpace(task.ChatID) == "" {
		return fmt.Errorf("timer %s: chat_id is required", task.ID)
	}
	if strings.TrimSpace(task.Prompt) == "" {
		return fmt.Errorf("timer %s: prompt is required", task.ID)
	}
	return ValidateSchedule(task.Schedule)
}

func ValidateSchedule(schedule Schedule) error {
	switch strings.TrimSpace(schedule.Kind) {
	case "interval":
		every, err := time.ParseDuration(strings.TrimSpace(schedule.Every))
		if err != nil {
			return fmt.Errorf("invalid interval duration: %w", err)
		}
		if every <= 0 {
			return fmt.Errorf("interval duration must be > 0")
		}
		return nil
	case "daily":
		if _, _, err := parseClock(schedule.At); err != nil {
			return err
		}
		_, err := loadLocation(schedule.Timezone)
		return err
	case "weekly":
		if _, _, err := parseClock(schedule.At); err != nil {
			return err
		}
		if len(schedule.Weekdays) == 0 {
			return fmt.Errorf("weekly schedule requires weekdays")
		}
		if _, err := normalizeWeekdays(schedule.Weekdays); err != nil {
			return err
		}
		_, err := loadLocation(schedule.Timezone)
		return err
	default:
		return fmt.Errorf("unsupported schedule kind %q", schedule.Kind)
	}
}

func loadLocation(name string) (*time.Location, error) {
	raw := strings.TrimSpace(name)
	if raw == "" {
		return time.Local, nil
	}
	loc, err := time.LoadLocation(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone %q: %w", raw, err)
	}
	return loc, nil
}

func parseClock(raw string) (int, int, error) {
	parts := strings.Split(strings.TrimSpace(raw), ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid time %q, expected HH:MM", raw)
	}
	hour := 0
	minute := 0
	if _, err := fmt.Sscanf(parts[0], "%d", &hour); err != nil {
		return 0, 0, fmt.Errorf("invalid hour in %q", raw)
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &minute); err != nil {
		return 0, 0, fmt.Errorf("invalid minute in %q", raw)
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf("invalid time %q, expected HH:MM", raw)
	}
	return hour, minute, nil
}

func normalizeWeekdays(raw []string) (map[time.Weekday]bool, error) {
	out := map[time.Weekday]bool{}
	for _, item := range raw {
		switch strings.ToLower(strings.TrimSpace(item)) {
		case "sun", "sunday", "0":
			out[time.Sunday] = true
		case "mon", "monday", "1":
			out[time.Monday] = true
		case "tue", "tues", "tuesday", "2":
			out[time.Tuesday] = true
		case "wed", "wednesday", "3":
			out[time.Wednesday] = true
		case "thu", "thurs", "thursday", "4":
			out[time.Thursday] = true
		case "fri", "friday", "5":
			out[time.Friday] = true
		case "sat", "saturday", "6":
			out[time.Saturday] = true
		default:
			return nil, fmt.Errorf("invalid weekday %q", item)
		}
	}
	return out, nil
}

func parseTaskTime(raw string) time.Time {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}
	}
	return t
}

func (t Task) createdTime() time.Time {
	return parseTaskTime(t.CreatedAt)
}

func (t Task) NextRunAfter(after time.Time) (time.Time, error) {
	switch strings.TrimSpace(t.Schedule.Kind) {
	case "interval":
		every, _ := time.ParseDuration(strings.TrimSpace(t.Schedule.Every))
		anchor := t.createdTime()
		if anchor.IsZero() {
			anchor = time.Now()
		}
		if after.Before(anchor) {
			return anchor.Add(every), nil
		}
		steps := int64(after.Sub(anchor) / every)
		next := anchor.Add(time.Duration(steps+1) * every)
		if !next.After(after) {
			next = next.Add(every)
		}
		return next, nil
	case "daily":
		loc, _ := loadLocation(t.Schedule.Timezone)
		hour, minute, _ := parseClock(t.Schedule.At)
		base := after.In(loc)
		candidate := time.Date(base.Year(), base.Month(), base.Day(), hour, minute, 0, 0, loc)
		if !candidate.After(base) {
			candidate = candidate.AddDate(0, 0, 1)
		}
		return candidate, nil
	case "weekly":
		loc, _ := loadLocation(t.Schedule.Timezone)
		hour, minute, _ := parseClock(t.Schedule.At)
		weekdays, _ := normalizeWeekdays(t.Schedule.Weekdays)
		base := after.In(loc)
		for i := 0; i < 8; i++ {
			day := base.AddDate(0, 0, i)
			candidate := time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, loc)
			if !candidate.After(base) {
				continue
			}
			if weekdays[candidate.Weekday()] {
				return candidate, nil
			}
		}
		return time.Time{}, fmt.Errorf("unable to compute next weekly run")
	default:
		return time.Time{}, fmt.Errorf("unsupported schedule kind %q", t.Schedule.Kind)
	}
}

func NextDue(task Task, st State, now time.Time) (time.Time, time.Time, error) {
	created := task.createdTime()
	if created.IsZero() {
		created = now
	}
	after := created.Add(-time.Second)
	lastScheduled := parseTaskTime(st.LastScheduledFor)
	if !lastScheduled.IsZero() {
		after = lastScheduled
	}
	next, err := task.NextRunAfter(after)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if next.After(now) {
		return time.Time{}, next, nil
	}
	following, err := task.NextRunAfter(next)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return next, following, nil
}

func CompactLogText(raw string, max int) string {
	text := strings.TrimSpace(strings.ReplaceAll(raw, "\r", ""))
	if text == "" {
		return ""
	}
	if len(text) <= max {
		return text
	}
	return strings.TrimSpace(text[:max]) + " ...(truncated)"
}

func (m *Manager) markRunning(id string, running bool) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if running {
		if m.running[id] {
			return false
		}
		m.running[id] = true
		return true
	}
	delete(m.running, id)
	return true
}

func (m *Manager) Run(ctx context.Context) error {
	if _, err := fmt.Fprintf(m.opts.Output, "[timer] scheduler started poll=%s\n", m.opts.PollInterval); err != nil {
		return err
	}
	m.tick(ctx, time.Now())
	ticker := time.NewTicker(m.opts.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_, _ = fmt.Fprintln(m.opts.Output, "[timer] scheduler stopped")
			return nil
		case now := <-ticker.C:
			m.tick(ctx, now)
		}
	}
}

func (m *Manager) tick(ctx context.Context, now time.Time) {
	tasks, err := m.store.ListTasks()
	if err != nil {
		_, _ = fmt.Fprintf(m.opts.Output, "[timer] list failed: %v\n", err)
		return
	}
	for _, task := range tasks {
		if !task.Enabled {
			continue
		}
		st, err := m.store.ReadState(task.ID)
		if err != nil {
			_, _ = fmt.Fprintf(m.opts.Output, "[timer] state read failed id=%s err=%v\n", task.ID, err)
			continue
		}
		dueAt, nextRun, err := NextDue(task, st, now)
		if err != nil {
			st.LastError = err.Error()
			st.UpdatedAt = now.UTC().Format(time.RFC3339)
			_ = m.store.WriteState(st)
			_, _ = fmt.Fprintf(m.opts.Output, "[timer] invalid task id=%s err=%v\n", task.ID, err)
			continue
		}
		st.NextRunAt = nextRun.UTC().Format(time.RFC3339)
		st.UpdatedAt = now.UTC().Format(time.RFC3339)
		_ = m.store.WriteState(st)
		if dueAt.IsZero() {
			continue
		}
		if !m.markRunning(task.ID, true) {
			continue
		}
		go func(task Task, scheduledFor time.Time) {
			defer m.markRunning(task.ID, false)
			if err := m.runTask(ctx, task, scheduledFor); err != nil {
				_, _ = fmt.Fprintf(m.opts.Output, "[timer] run failed id=%s err=%v\n", task.ID, err)
			}
		}(task, dueAt)
	}
}

func (m *Manager) RunTaskNow(ctx context.Context, id string) error {
	task, err := m.store.ReadTask(id)
	if err != nil {
		return err
	}
	if err := ValidateTask(task); err != nil {
		return err
	}
	return m.runTask(ctx, task, time.Now())
}

func (m *Manager) runTask(ctx context.Context, task Task, scheduledFor time.Time) error {
	now := time.Now().UTC()
	logPath := m.store.LogPath(task.ID)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close()

	payloadPath := m.store.TaskPath(task.ID)
	cmd := exec.CommandContext(ctx, m.opts.NodeBin, filepath.Join(m.opts.RepoRoot, "tools", "feishu_ws_bot.js"), "--account", task.Account, "--timer-task-file", payloadPath)
	cmd.Dir = m.opts.RepoRoot
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	startLine := fmt.Sprintf("[%s] timer id=%s scheduled_for=%s chat_id=%s\n", now.Format(time.RFC3339), task.ID, scheduledFor.UTC().Format(time.RFC3339), task.ChatID)
	if _, err := logFile.WriteString(startLine); err != nil {
		return err
	}

	runErr := cmd.Run()
	finishedAt := time.Now().UTC()

	st, _ := m.store.ReadState(task.ID)
	st.ID = task.ID
	st.LastRunAt = now.Format(time.RFC3339)
	st.LastFinishedAt = finishedAt.Format(time.RFC3339)
	st.LastScheduledFor = scheduledFor.UTC().Format(time.RFC3339)
	st.LastExitCode = 0
	st.LastError = ""

	logBytes, _ := os.ReadFile(logPath)
	st.LastOutput = CompactLogText(string(logBytes), 600)
	if runErr != nil {
		st.LastExitCode = 1
		st.LastError = runErr.Error()
	} else {
		st.LastSuccessAt = finishedAt.Format(time.RFC3339)
	}
	if _, nextRun, err := NextDue(task, st, finishedAt); err == nil && !nextRun.IsZero() {
		st.NextRunAt = nextRun.UTC().Format(time.RFC3339)
	}
	st.UpdatedAt = finishedAt.Format(time.RFC3339)
	_ = m.store.WriteState(st)
	return runErr
}

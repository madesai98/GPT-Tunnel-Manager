package logging

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/config"
)

type Level string

const (
	Trace Level = "trace"
	Debug Level = "debug"
	Info  Level = "info"
	Warn  Level = "warn"
	Error Level = "error"
)

var rank = map[Level]int{Trace: 0, Debug: 1, Info: 2, Warn: 3, Error: 4}

type Event struct {
	Timestamp time.Time      `json:"timestamp"`
	Level     Level          `json:"level"`
	Source    string         `json:"source"`
	Component string         `json:"component"`
	Message   string         `json:"message"`
	Fields    map[string]any `json:"fields,omitempty"`
}

type Redactor struct {
	mu     sync.RWMutex
	values []string
}

func NewRedactor() *Redactor { return &Redactor{} }

func (r *Redactor) Register(v []byte) {
	s := string(v)
	if len(s) < 3 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.values {
		if existing == s {
			return
		}
	}
	r.values = append(r.values, s)
	sort.Slice(r.values, func(i, j int) bool { return len(r.values[i]) > len(r.values[j]) })
}

func (r *Redactor) String(s string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, v := range r.values {
		s = strings.ReplaceAll(s, v, "[REDACTED]")
	}
	return redactAuthorization(s)
}

func redactAuthorization(s string) string {
	lower := strings.ToLower(s)
	for {
		idx := strings.Index(lower, "authorization:")
		if idx < 0 {
			return s
		}
		end := strings.IndexByte(s[idx:], '\n')
		if end < 0 {
			end = len(s) - idx
		}
		s = s[:idx] + "Authorization: [REDACTED]" + s[idx+end:]
		lower = strings.ToLower(s)
	}
}

type Ring struct {
	mu     sync.RWMutex
	events []Event
	bytes  int
	max    int
}

func NewRing(maxMB int) *Ring {
	if maxMB <= 0 {
		maxMB = 25
	}
	return &Ring{max: maxMB * 1024 * 1024}
}

func (r *Ring) Add(e Event) {
	b, _ := json.Marshal(e)
	n := len(b)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
	r.bytes += n
	r.evictLocked()
}

func (r *Ring) SetMaxMB(maxMB int) {
	if maxMB <= 0 {
		maxMB = 25
	}
	r.mu.Lock()
	r.max = maxMB * 1024 * 1024
	r.evictLocked()
	r.mu.Unlock()
}

func (r *Ring) evictLocked() {
	for r.bytes > r.max && len(r.events) > 1 {
		old, _ := json.Marshal(r.events[0])
		r.bytes -= len(old)
		r.events = r.events[1:]
	}
}

func (r *Ring) Snapshot() []Event {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Event, len(r.events))
	copy(out, r.events)
	return out
}

func (r *Ring) Clear() {
	r.mu.Lock()
	r.events = nil
	r.bytes = 0
	r.mu.Unlock()
}

type Logger struct {
	mu       sync.RWMutex
	root     string
	redactor *Redactor
	ring     *Ring
	capture  Level
	disk     *diskSink
}

func New(root, capture string, memoryMB int, writeDisk bool, diskMin string, maxFileMB, keep int) (*Logger, error) {
	l := &Logger{root: root, redactor: NewRedactor(), ring: NewRing(memoryMB), capture: parseLevel(capture)}
	if writeDisk {
		d, err := newDiskSink(filepath.Join(root, "logs", "manager"), parseLevel(diskMin), maxFileMB, keep)
		if err != nil {
			return nil, err
		}
		l.disk = d
	}
	return l, nil
}

func parseLevel(s string) Level {
	if level, ok := knownLevel(s); ok {
		return level
	}
	return Info
}

func knownLevel(s string) (Level, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "trace":
		return Trace, true
	case "debug":
		return Debug, true
	case "info", "information":
		return Info, true
	case "warn", "warning":
		return Warn, true
	case "error", "fatal", "panic", "dpanic":
		return Error, true
	default:
		return Info, false
	}
}

func (l *Logger) Redactor() *Redactor { return l.redactor }
func (l *Logger) Ring() *Ring         { return l.ring }

func (l *Logger) Log(level Level, source, component, msg string, fields map[string]any) {
	timestamp := time.Now().UTC()
	if component == "Tunnel Client" {
		timestamp, level, component, msg, fields = normalizeTunnelClientEvent(timestamp, level, component, msg, fields)
	}

	l.mu.RLock()
	capture := l.capture
	disk := l.disk
	l.mu.RUnlock()
	if rank[level] < rank[capture] {
		return
	}

	e := Event{
		Timestamp: timestamp,
		Level:     level,
		Source:    l.redactor.String(source),
		Component: l.redactor.String(component),
		Message:   l.redactor.String(msg),
		Fields:    redactFields(l.redactor, fields),
	}
	l.ring.Add(e)
	if disk != nil {
		_ = disk.write(e)
	}
}

func normalizeTunnelClientEvent(timestamp time.Time, level Level, component, msg string, fields map[string]any) (time.Time, Level, string, string, map[string]any) {
	var structured map[string]any
	if json.Unmarshal([]byte(msg), &structured) != nil {
		return timestamp, level, component, msg, fields
	}

	structuredMessage, ok := structured["msg"].(string)
	if !ok {
		structuredMessage, ok = structured["message"].(string)
	}
	if !ok {
		return timestamp, level, component, msg, fields
	}

	if rawLevel, ok := structured["level"].(string); ok {
		if parsed, recognized := knownLevel(rawLevel); recognized {
			level = parsed
		}
	}

	for _, key := range []string{"time", "timestamp"} {
		if rawTime, ok := structured[key].(string); ok {
			if parsed, err := time.Parse(time.RFC3339Nano, rawTime); err == nil {
				timestamp = parsed
				break
			}
		}
	}

	if nestedComponent, ok := structured["component"].(string); ok {
		nestedComponent = strings.TrimSpace(nestedComponent)
		if nestedComponent != "" && !strings.EqualFold(nestedComponent, component) {
			component += "/" + nestedComponent
		}
	}

	for _, key := range []string{"time", "timestamp", "level", "msg", "message", "component"} {
		delete(structured, key)
	}

	merged := make(map[string]any, len(structured)+len(fields))
	for key, value := range structured {
		merged[key] = value
	}
	for key, value := range fields {
		merged[key] = value
	}
	if len(merged) == 0 {
		merged = nil
	}

	return timestamp, level, component, structuredMessage, merged
}

func redactFields(r *Redactor, m map[string]any) map[string]any {
	if len(m) == 0 {
		return nil
	}
	o := make(map[string]any, len(m))
	for k, v := range m {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "authorization") || strings.Contains(lk, "token") || strings.Contains(lk, "secret") || strings.Contains(lk, "api_key") {
			o[k] = "[REDACTED]"
			continue
		}
		o[k] = redactValue(r, v)
	}
	return o
}

func redactValue(r *Redactor, value any) any {
	switch x := value.(type) {
	case string:
		return r.String(x)
	case map[string]any:
		return redactFields(r, x)
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = redactValue(r, item)
		}
		return out
	default:
		return value
	}
}

func (l *Logger) Reconfigure(c config.LoggingConfig) error {
	var next *diskSink
	var err error
	if c.WriteToDisk {
		next, err = newDiskSink(filepath.Join(l.root, "logs", "manager"), parseLevel(c.DiskMinimumLevel), c.MaximumFileSizeMB, c.KeepFiles)
		if err != nil {
			return err
		}
	}
	l.mu.Lock()
	old := l.disk
	l.disk = next
	l.capture = parseLevel(c.CaptureLevel)
	l.ring.SetMaxMB(c.MemoryLimitMB)
	l.mu.Unlock()
	if old != nil {
		return old.close()
	}
	return nil
}

func (l *Logger) Close() error {
	l.mu.Lock()
	disk := l.disk
	l.disk = nil
	l.mu.Unlock()
	if disk != nil {
		return disk.close()
	}
	return nil
}

func (l *Logger) ExportJSONL(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, e := range l.ring.Snapshot() {
		if err := enc.Encode(e); err != nil {
			return err
		}
	}
	return nil
}

func (l *Logger) ExportText(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, e := range l.ring.Snapshot() {
		if _, err := fmt.Fprintf(f, "%s %-5s %-18s %-14s %s\n", e.Timestamp.Format(time.RFC3339), strings.ToUpper(string(e.Level)), e.Source, e.Component, e.Message); err != nil {
			return err
		}
	}
	return nil
}

type diskSink struct {
	mu       sync.Mutex
	dir      string
	min      Level
	maxBytes int64
	keep     int
	f        *os.File
}

func newDiskSink(dir string, min Level, maxMB, keep int) (*diskSink, error) {
	if maxMB <= 0 {
		maxMB = 10
	}
	if keep <= 0 {
		keep = 5
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	d := &diskSink{dir: dir, min: min, maxBytes: int64(maxMB) * 1024 * 1024, keep: keep}
	return d, d.open()
}

func (d *diskSink) open() error {
	f, err := os.OpenFile(filepath.Join(d.dir, "manager.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		d.f = f
	}
	return err
}

func (d *diskSink) write(e Event) error {
	if rank[e.Level] < rank[d.min] {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.f == nil {
		if err := d.open(); err != nil {
			return err
		}
	}
	if st, err := d.f.Stat(); err == nil && st.Size() >= d.maxBytes {
		if err := d.rotate(); err != nil {
			return err
		}
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = d.f.Write(b)
	return err
}

func (d *diskSink) rotate() error {
	if d.f != nil {
		_ = d.f.Close()
		d.f = nil
	}
	_ = os.Remove(filepath.Join(d.dir, fmt.Sprintf("manager.%d.jsonl", d.keep)))
	for n := d.keep - 1; n >= 1; n-- {
		_ = os.Rename(filepath.Join(d.dir, fmt.Sprintf("manager.%d.jsonl", n)), filepath.Join(d.dir, fmt.Sprintf("manager.%d.jsonl", n+1)))
	}
	_ = os.Rename(filepath.Join(d.dir, "manager.jsonl"), filepath.Join(d.dir, "manager.1.jsonl"))
	return d.open()
}

func (d *diskSink) close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.f != nil {
		err := d.f.Sync()
		closeErr := d.f.Close()
		d.f = nil
		if err != nil {
			return err
		}
		return closeErr
	}
	return nil
}

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// ---- log ----

var logMu sync.Mutex

func logf(format string, a ...any) {
	logMu.Lock()
	defer logMu.Unlock()
	fmt.Fprintf(os.Stderr, format+"\n", a...)
}

// logger prefixa as mensagens com [n/total] quando há vários vídeos.
type logger struct {
	prefix   string
	inline   bool // progresso na mesma linha (\r); só com um vídeo
	lastStep int
}

func newLogger(i, total int) *logger {
	if total == 1 {
		return &logger{inline: true}
	}
	return &logger{prefix: fmt.Sprintf("[%d/%d] ", i+1, total)}
}

func (l *logger) printf(format string, a ...any) { logf(l.prefix+format, a...) }

func (l *logger) progress(pct string) {
	if l.inline {
		logMu.Lock()
		fmt.Fprintf(os.Stderr, "\r  %s%%", pct)
		logMu.Unlock()
		return
	}
	// Com vários vídeos em paralelo, \r embaralharia as linhas: avisa a cada 25%.
	var n int
	fmt.Sscan(pct, &n)
	if step := n / 25; step > l.lastStep {
		l.lastStep = step
		l.printf("  %d%%", n)
	}
}

func (l *logger) progressDone() {
	if l.inline {
		logMu.Lock()
		fmt.Fprint(os.Stderr, "\r      \r")
		logMu.Unlock()
	}
}

// ---- CPU ----

// defaultThreads devolve o número de núcleos físicos disponíveis. No whisper,
// usar também os hyperthreads deixou a transcrição ~2x mais lenta e instável
// (i5-1135G7: 4 threads 43–48 s; 8 threads 50–95 s no mesmo trecho).
// GOMAXPROCS já respeita o limite de CPU do cgroup (contêineres) e a afinidade.
func defaultThreads() int {
	n := runtime.NumCPU()
	if cores := physicalCores(); cores > 0 {
		n = cores
	}
	return max(1, min(n, runtime.GOMAXPROCS(0)))
}

// ---- texto e arquivos ----

func normalizeLang(l string) string {
	l = strings.ToLower(strings.TrimSpace(l))
	if l == "" || l == "auto" {
		return l
	}
	if i := strings.IndexAny(l, "-_"); i > 0 {
		l = l[:i]
	}
	return l
}

func executableDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return filepath.Dir(exe)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

var unsafeChars = regexp.MustCompile(`[/\\:*?"<>|\x00-\x1f]+`)

// Nomes que o Windows reserva para dispositivos, com ou sem extensão.
var reservedNames = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[0-9¹²³]|lpt[0-9¹²³])(\..*)?$`)

// safeName transforma o título num nome de arquivo válido em Linux, macOS e
// Windows: sem caracteres proibidos, sem nomes reservados (CON, NUL...), sem
// ponto ou espaço no fim e com até 120 caracteres.
func safeName(s string) string {
	s = strings.Join(strings.Fields(unsafeChars.ReplaceAllString(s, " ")), " ")
	if r := []rune(s); len(r) > 120 {
		s = string(r[:120])
	}
	s = strings.TrimRight(s, ". ")
	if s == "" {
		return "video"
	}
	if reservedNames.MatchString(s) {
		s = "_" + s
	}
	return s
}

func joinLines(s string) string {
	var parts []string
	for l := range strings.Lines(s) {
		if l = strings.TrimSpace(l); l != "" {
			parts = append(parts, l)
		}
	}
	return wrapParagraphs(parts)
}

func fmtDuration(sec float64) string {
	d := time.Duration(sec) * time.Second
	if d >= time.Hour {
		return fmt.Sprintf("%dh%02dm%02ds", int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

func lastLine(s string, err error) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[i+1:])
	}
	if s != "" {
		return s
	}
	return err.Error()
}

package main

import (
	"bufio"
	"html"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

type cue struct {
	start, end string // "00:01:02.345"
	text       string
}

// recentLines é quantas linhas anteriores entram na deduplicação.
const recentLines = 4

// parseVTT lê um WebVTT do YouTube. As legendas automáticas repetem a linha
// anterior em cada cue ("rolagem"); aqui cada cue fica só com o texto novo.
//
// O laço evita regex e maps: em legendas automáticas de horas de vídeo isso
// corta ~75% das alocações e é ~3,5x mais rápido (ver BenchmarkParseVTT).
func parseVTT(r io.Reader) ([]cue, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var (
		cues   []cue
		cur    cue
		inCue  bool
		lines  []string
		recent [recentLines]string // últimas linhas emitidas (anel)
		nRec   int
		fresh  []string
	)

	flush := func() {
		if !inCue {
			return
		}
		fresh = fresh[:0]
		for _, l := range lines {
			l = cleanLine(l)
			if l == "" || slices.Contains(recent[:], l) {
				continue
			}
			fresh = append(fresh, l)
			recent[nRec%recentLines] = l
			nRec++
		}
		if len(fresh) > 0 {
			cur.text = strings.Join(fresh, " ")
			cues = append(cues, cur)
		}
		inCue, lines = false, lines[:0]
	}

	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if start, end, ok := parseTiming(line); ok {
			flush()
			cur, inCue = cue{start: start, end: end}, true
			continue
		}
		if !inCue {
			continue // cabeçalho, NOTE, STYLE, ids
		}
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		lines = append(lines, line)
	}
	flush()
	return cues, sc.Err()
}

// parseTiming reconhece "00:01.000 --> 00:00:03.500 align:start ..." e
// devolve os tempos no formato completo HH:MM:SS.mmm.
func parseTiming(line string) (start, end string, ok bool) {
	a, rest, found := strings.Cut(line, " --> ")
	if !found {
		return "", "", false
	}
	rest = strings.TrimLeft(rest, " \t")
	b, _, _ := strings.Cut(rest, " ")
	a = strings.TrimSpace(a)
	if !isTimestamp(a) || !isTimestamp(b) {
		return "", "", false
	}
	return fullTS(a), fullTS(b), true
}

// isTimestamp aceita [H]H:MM:SS.mmm e MM:SS.mmm.
func isTimestamp(s string) bool {
	hms, ms, ok := strings.Cut(s, ".")
	if !ok || len(ms) != 3 || !digits(ms) {
		return false
	}
	a, rest, ok := strings.Cut(hms, ":")
	if !ok {
		return false
	}
	b, c, ok := strings.Cut(rest, ":")
	if !ok { // MM:SS
		return len(a) == 2 && len(rest) == 2 && digits(a) && digits(rest)
	}
	return (len(a) == 1 || len(a) == 2) && len(b) == 2 && len(c) == 2 && digits(a) && digits(b) && digits(c)
}

func digits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

func fullTS(ts string) string {
	if strings.Count(ts, ":") == 1 {
		return "00:" + ts
	}
	if len(ts) == len("0:00:00.000") {
		return "0" + ts
	}
	return ts
}

// cleanLine tira tags (<c>, <00:00:01.000>, <i>), decodifica entidades HTML
// e junta espaços. Só aloca quando a linha precisa mudar.
func cleanLine(l string) string {
	if strings.IndexByte(l, '<') >= 0 {
		var b strings.Builder
		b.Grow(len(l))
		for {
			i := strings.IndexByte(l, '<')
			if i < 0 {
				break
			}
			b.WriteString(l[:i])
			j := strings.IndexByte(l[i:], '>')
			if j < 0 {
				l = l[i:] // "<" sem fechamento: mantém como texto
				break
			}
			l = l[i+j+1:]
		}
		b.WriteString(l)
		l = b.String()
	}
	if strings.IndexByte(l, '&') >= 0 {
		l = html.UnescapeString(l)
	}
	if needsSpaceFix(l) {
		l = strings.Join(strings.Fields(l), " ")
	}
	return l
}

// needsSpaceFix diz se a linha tem espaço no começo/fim, espaços repetidos
// ou espaços que não são ' ' (tab, NBSP...).
func needsSpaceFix(s string) bool {
	prevSpace := true
	for _, r := range s {
		sp := unicode.IsSpace(r)
		if sp && (r != ' ' || prevSpace) {
			return true
		}
		prevSpace = sp
	}
	return prevSpace
}

func cuesToText(cues []cue) string {
	parts := make([]string, len(cues))
	for i, c := range cues {
		parts[i] = c.text
	}
	return wrapParagraphs(parts)
}

// wrapParagraphs junta os trechos em parágrafos de ~600 caracteres,
// quebrando depois de um ponto final quando possível.
func wrapParagraphs(parts []string) string {
	total := 0
	for _, p := range parts {
		total += len(p) + 1
	}
	var b strings.Builder
	b.Grow(total + total/300)
	paraLen := 0
	for _, p := range parts {
		if paraLen > 0 {
			b.WriteByte(' ')
			paraLen++
		}
		b.WriteString(p)
		paraLen += len(p)
		if paraLen >= 600 && strings.ContainsAny(p[len(p)-1:], ".!?") {
			b.WriteString("\n\n")
			paraLen = 0
		}
	}
	return strings.TrimSpace(b.String())
}

func cuesToSRT(cues []cue) string {
	var b strings.Builder
	size := 0
	for _, c := range cues {
		size += len(c.text) + 40
	}
	b.Grow(size)
	var num [20]byte
	for i, c := range cues {
		b.Write(strconv.AppendInt(num[:0], int64(i+1), 10))
		b.WriteByte('\n')
		writeSRTTime(&b, c.start)
		b.WriteString(" --> ")
		writeSRTTime(&b, c.end)
		b.WriteByte('\n')
		b.WriteString(c.text)
		b.WriteString("\n\n")
	}
	return b.String()
}

// writeSRTTime escreve o tempo com vírgula nos milissegundos, como o SRT exige.
func writeSRTTime(b *strings.Builder, ts string) {
	if i := strings.LastIndexByte(ts, '.'); i >= 0 {
		b.WriteString(ts[:i])
		b.WriteByte(',')
		b.WriteString(ts[i+1:])
		return
	}
	b.WriteString(ts)
}

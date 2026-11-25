package main

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func TestParseVTTAutoCaptionsDedup(t *testing.T) {
	in := `WEBVTT
Kind: captions
Language: pt

00:00:00.160 --> 00:00:02.310 align:start position:0%

olá<00:00:00.480><c> pessoal</c><00:00:00.800><c> tudo</c><00:00:01.120><c> bem</c>

00:00:02.310 --> 00:00:02.320 align:start position:0%
olá pessoal tudo bem


00:00:02.320 --> 00:00:04.950 align:start position:0%
olá pessoal tudo bem
hoje<00:00:02.640><c> vamos</c><00:00:03.000><c> falar</c>

00:00:04.950 --> 00:00:04.960 align:start position:0%
hoje vamos falar

`
	cues, err := parseVTT(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	got := cuesToText(cues)
	want := "olá pessoal tudo bem hoje vamos falar"
	if got != want {
		t.Fatalf("texto:\n got %q\nwant %q", got, want)
	}
	if len(cues) != 2 || cues[1].start != "00:00:02.320" {
		t.Fatalf("cues inesperados: %+v", cues)
	}
}

func TestParseVTTManualShortTimestamps(t *testing.T) {
	in := "WEBVTT\n\n1\n00:01.000 --> 00:03.500\nPrimeira &amp; linha\n\n2\n00:04.000 --> 00:06.000\n<i>Segunda</i>\n"
	cues, err := parseVTT(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	srt := cuesToSRT(cues)
	want := "1\n00:00:01,000 --> 00:00:03,500\nPrimeira & linha\n\n2\n00:00:04,000 --> 00:00:06,000\nSegunda\n\n"
	if srt != want {
		t.Fatalf("srt:\n got %q\nwant %q", srt, want)
	}
}

func TestPickSubtitle(t *testing.T) {
	info := &videoInfo{
		Language:          "en",
		Subtitles:         map[string][]subTrack{"pt-BR": {{Ext: "vtt"}}},
		AutomaticCaptions: map[string][]subTrack{"en-orig": {}, "en": {}, "pt": {}, "es": {}},
	}
	cases := []struct {
		lang, key string
		auto, ok  bool
	}{
		{"pt", "pt-BR", false, true}, // manual vence
		{"en", "en-orig", true, true},
		{"es", "", false, false}, // "es" automático seria tradução: recusa
	}
	for _, c := range cases {
		key, auto, ok := pickSubtitle(info, c.lang)
		if key != c.key || auto != c.auto || ok != c.ok {
			t.Errorf("%s: got (%q,%v,%v) want (%q,%v,%v)", c.lang, key, auto, ok, c.key, c.auto, c.ok)
		}
	}
}

func TestSafeName(t *testing.T) {
	if got := safeName(`Como fazer: A/B "teste"?`); got != "Como fazer A B teste" {
		t.Fatalf("got %q", got)
	}
}

// autoVTT gera uma legenda automática de ~1 h no formato do YouTube
// (palavras com tempo e linha anterior repetida a cada cue).
func autoVTT(n int) string {
	var b strings.Builder
	b.WriteString("WEBVTT\nKind: captions\nLanguage: pt\n\n")
	prev := ""
	for i := range n {
		s := fmtTS(i * 2000)
		e := fmtTS(i*2000 + 1990)
		line := fmt.Sprintf("frase%d<%s><c> palavra</c><%s><c> outra &amp; mais</c>", i, s, s)
		fmt.Fprintf(&b, "%s --> %s align:start position:0%%\n%s\n%s\n\n", s, e, prev, line)
		fmt.Fprintf(&b, "%s --> %s align:start position:0%%\nfrase%d palavra outra & mais\n\n\n", e, fmtTS(i*2000+2000), i)
		prev = fmt.Sprintf("frase%d palavra outra & mais", i)
	}
	return b.String()
}

func fmtTS(ms int) string {
	return fmt.Sprintf("%02d:%02d:%02d.%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
}

func BenchmarkParseVTT(b *testing.B) {
	in := autoVTT(1800) // 1 hora
	b.SetBytes(int64(len(in)))
	b.ReportAllocs()
	for b.Loop() {
		cues, err := parseVTT(strings.NewReader(in))
		if err != nil || len(cues) != 1800 {
			b.Fatalf("cues=%d err=%v", len(cues), err)
		}
		_ = cuesToText(cues)
		_ = cuesToSRT(cues)
	}
}

func TestParseTiming(t *testing.T) {
	cases := []struct {
		in, start, end string
		ok             bool
	}{
		{"00:00:01.000 --> 00:00:02.500 align:start position:0%", "00:00:01.000", "00:00:02.500", true},
		{"01:02.000 --> 1:03:04.005", "00:01:02.000", "01:03:04.005", true},
		{"00:01.000 --> 00:02.000", "00:00:01.000", "00:00:02.000", true},
		{"texto --> com seta", "", "", false},
		{"00:01.00 --> 00:02.000", "", "", false},
		{"NOTE 00:01.000", "", "", false},
	}
	for _, c := range cases {
		s, e, ok := parseTiming(c.in)
		if s != c.start || e != c.end || ok != c.ok {
			t.Errorf("%q: got (%q,%q,%v)", c.in, s, e, ok)
		}
	}
}

func TestCleanLine(t *testing.T) {
	cases := map[string]string{
		"simples":                         "simples",
		"<i>itálico</i> e &amp; entidade": "itálico e & entidade",
		"a<00:00:01.000><c> b</c>":        "a b",
		"  muitos   espaços\t":            "muitos espaços",
		"nbsp&nbsp;aqui":                  "nbsp aqui",
		"menor < sem fechar":              "menor < sem fechar",
		"<c></c>":                         "",
	}
	for in, want := range cases {
		if got := cleanLine(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestRing(t *testing.T) {
	var r ring
	if r.join("|") != "" {
		t.Fatal("anel vazio")
	}
	for i := range 7 {
		r.add(fmt.Sprint(i))
	}
	if got := r.join("|"); got != "2|3|4|5|6" {
		t.Fatalf("got %q", got)
	}
}

func TestDefaultThreads(t *testing.T) {
	if n := defaultThreads(); n < 1 || n > runtime.NumCPU() {
		t.Fatalf("threads fora do intervalo: %d", n)
	}
}

func TestSafeNameDots(t *testing.T) {
	for _, in := range []string{"..", ".", "  ", "/"} {
		if got := safeName(in); got != "video" {
			t.Errorf("%q: got %q", in, got)
		}
	}
}

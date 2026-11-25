# AGENTS.md

Instruções para agentes de IA que trabalham neste repositório.

Este repositório tem dois usos. Identifique qual o usuário quer:

- **Analisar vídeos** (o mais comum). O usuário manda um ou mais links do YouTube e quer entender o conteúdo: resumo, insights, detalhes, checagem de fatos, pesquisa complementar. Siga a parte 1.
- **Mexer no código** da ferramenta `yt-transcript`. Siga a parte 2.

---

## Parte 1: Analisar vídeos do YouTube

### 1.1 Achar a ferramenta

Teste nesta ordem e use a primeira que responder a `-h`:

1. `yt-transcript` (instalada no PATH)
2. `./bin/yt-transcript` (Linux/macOS) ou `.\bin\yt-transcript.exe` (Windows)
3. Se nenhuma existir e o Go estiver instalado, compile:
   - Linux/macOS: `make build`, ou `go build -o bin/yt-transcript .`
   - Windows: `go build -o bin\yt-transcript.exe .`

Antes da primeira transcrição, rode `<ferramenta> setup`. Ele confere `yt-dlp` e `ffmpeg`, acha ou baixa o whisper e baixa os modelos. Se faltar algo, ele imprime o comando de instalação para o sistema do usuário: mostre esse comando ao usuário e peça que ele rode (instalar programas no sistema é decisão dele).

### 1.2 Pegar a transcrição

```bash
yt-transcript -srt -o transcricoes "https://www.youtube.com/watch?v=ID"
```

- **Sempre use `-srt`.** O `.srt` tem os tempos de cada fala, e você precisa deles para citar trechos.
- Salve em `transcricoes/`. Esta pasta fica fora do git.
- **O stdout traz só os caminhos dos arquivos gerados**, um por linha (`.txt` e depois `.srt`). O progresso vai para o stderr. Leia os arquivos pelos caminhos impressos; não tente adivinhar o nome.
- Vários vídeos de uma vez: passe todos os links no mesmo comando. Eles são processados em paralelo.
- Playlists não são expandidas: peça os links individuais ao usuário.

Tempo esperado:

- Com legenda do YouTube: ~3 s.
- Sem legenda, com a IA local: ~1/8 da duração do vídeo num notebook de 4 núcleos (um vídeo de 1 h leva ~8 min). Para vídeos longos, avise o usuário antes e rode o comando em segundo plano se a sua ferramenta permitir.

O `.txt` começa com um cabeçalho:

```
<título>
https://www.youtube.com/watch?v=<ID>
Fonte: legenda manual do YouTube (pt-BR)   ← ou "legenda automática" ou "whisper.cpp (ggml-base.bin)"
```

**A fonte diz o quanto confiar no texto:**

| Fonte | Confiabilidade |
|---|---|
| legenda manual | alta: foi revisada por alguém |
| legenda automática | média: erra nomes próprios, números e termos técnicos |
| whisper.cpp | média: erros parecidos (~10% das palavras com o modelo `base`) |

Toda transcrição passa pela revisão da seção 1.3 antes da análise. Com fonte automática ou whisper, a revisão é obrigatória e profunda.

Opções úteis:

| Opção | Quando usar |
|---|---|
| `-lang en` | o YouTube informou o idioma errado |
| `-force-ai` | a legenda do YouTube está ruim ou incompleta |
| `-model small -beam 1` | precisão importa mais que tempo (erro cai de ~10% para ~7%, leva ~2x mais tempo) |

Problemas comuns:

- "Sign in to confirm you're not a bot" ou HTTP 403: o `yt-dlp` está desatualizado. Peça ao usuário para atualizá-lo (`pipx upgrade yt-dlp`, `brew upgrade yt-dlp` ou `winget upgrade yt-dlp.yt-dlp`).
- Vídeo privado, com restrição de idade ou de membros: não dá para baixar. Avise o usuário.

### 1.3 Revisar e corrigir a transcrição

A transcrição bruta tem erros. Antes de analisar, corrija o máximo que conseguir: a análise, as citações e a checagem de fatos usam o texto revisado. Um nome errado na transcrição vira uma pesquisa errada e uma citação errada.

**Junte contexto primeiro.** A descrição do vídeo costuma trazer nomes, empresas, livros e links escritos do jeito certo:

```bash
yt-dlp --skip-download --no-warnings --print "%(channel)s" --print "%(upload_date)s" --print "%(description)s" "<url>"
```

Use também o título, o canal e, se tiver busca na web, pesquise o palestrante e o assunto para conhecer os nomes e termos da área.

**O que corrigir:**

| Erro típico | Exemplo | Como resolver |
|---|---|---|
| Nomes de pessoas, empresas e marcas | "Elon Mask", "a NVídia" | descrição do vídeo, busca na web |
| Termos técnicos e jargão | "cubernetes", "machine lerning" | conhecimento da área e contexto da frase |
| Palavras estrangeiras no meio do português | "o feed bec do cliente" | escreva o termo original (*feedback*) |
| Palavras que soam iguais | "concerto" × "conserto", "mais" × "mas" | sentido da frase |
| Números, datas, valores e unidades | "dois mil e vinte" → 2020; "15%" que deveria ser "50%" | plausibilidade e contexto; confira dados citados |
| Siglas | "a ONG" × "a ONU" | contexto |
| Palavras cortadas ou juntadas | "em quanto", "derrepente" | ortografia |
| Mesmo nome escrito de formas diferentes | "Ana Cláudia" e "Ana Claudia" | escolha a forma certa e use em todo o texto |
| Pontuação e maiúsculas | a legenda automática do YouTube às vezes vem **sem pontuação nenhuma** (depende do vídeo) | pontue pelo sentido e pelas pausas do `.srt`; se já vier pontuada, só corrija o que estiver errado |
| Alucinações do whisper | frase repetida várias vezes seguidas, "Legendas pela comunidade Amara.org", "Obrigado por assistir" no meio de silêncio | remova |

**Limites da revisão:**

- **Corrija a transcrição, não a fala.** Mantenha as palavras e o jeito de falar do palestrante. Não reescreva frases, não troque gírias, não corrija a gramática da fala. Pode tirar gaguejos e repetições puras ("eu eu eu acho" → "eu acho").
- **Não mude o sentido.** Se o palestrante disse um dado errado, a transcrição continua com o dado errado. Isso é assunto da checagem de fatos, não da revisão.
- **Na dúvida, não chute.** Mantenha o trecho como está e marque com `[?]`, ou dê a melhor hipótese: `[provável: Kahneman]`. Trecho impossível de entender vira `[inaudível]`.
- Legenda manual: faça só uma revisão leve (pontuação, nomes óbvios). Ela já foi revisada por alguém.

**Como fazer em transcrições longas:** revise em partes, na ordem, e mantenha um glossário (nome ou termo → grafia certa) que vale para todas as partes. Assim a mesma pessoa não fica com três grafias diferentes.

**Organize em parágrafos** por assunto e, quando der para identificar, separe quem fala (entrevistas e podcasts): `**Entrevistador:**`, `**Fulano:**`.

**Onde fica:** a transcrição revisada não vira arquivo separado. Ela entra no fim do `.md` da análise, na parte **Anexo** (veja o modelo na seção 1.4), com um tempo do `.srt` a cada parágrafo, a tabela de correções e a lista de trechos incertos. Na tabela, liste as correções que mudam o entendimento (nomes, termos, números, palavras trocadas); pontuação e maiúsculas não precisam ir para a tabela.

### 1.4 Analisar

Trabalhe sobre a transcrição revisada (seção 1.3), nunca sobre a bruta. Leia ela inteira antes de escrever. Se ela não couber de uma vez, leia em partes na ordem e faça anotações por parte (tema, tempo de início, pontos principais) antes de juntar tudo. Não analise só o começo.

**A entrega é sempre um único arquivo Markdown por vídeo:** `analises/<mesmo nome do .txt>.md`. Ele reúne tudo: a análise completa, os insights, a checagem de fatos e, no anexo, a transcrição revisada. Quem abrir esse arquivo não precisa de mais nada.

- Todo link de vídeo gera esse `.md` completo, seja qual for o pedido ("resume", "analisa", só o link). O pedido muda o que você destaca no chat, não o arquivo.
- Pergunta específica ("o que ele fala sobre X?", "lista as ferramentas citadas"): responda no chat com tempos e citações. Se o vídeo ainda não tem `.md`, gere-o também.
- Se o arquivo já existir (vídeo analisado antes), atualize em vez de criar outro.
- Escreva no idioma em que o usuário falou com você, mesmo que o vídeo seja em outro. A transcrição revisada do anexo fica no idioma original do vídeo.
- Outro lugar ou formato só se o usuário pedir.

#### Modelo do arquivo

```markdown
# <Título do vídeo>

- **Link:** <url>
- **Canal / palestrante:** <se der para identificar>
- **Duração:** <hh:mm:ss>
- **Fonte da transcrição:** <manual / automática / whisper>, confiabilidade <alta/média>
- **Revisão da transcrição:** <N> correções, <M> trechos incertos (ver Anexo)
- **Analisado em:** <data de hoje>

## TL;DR
3 a 5 frases: a tese central e a conclusão do vídeo.

## Estrutura do vídeo
| Tempo | Parte | Do que trata |
|---|---|---|
| [00:00](url&t=0s) | Abertura | ... |

## Pontos principais
O argumento em ordem, com tempos. Explique o raciocínio, não só o tema
("ele mostra que X porque Y" em vez de "ele fala de X").

## Insights
O que não é óbvio: ideias contraintuitivas, conexões entre partes do vídeo,
implicações que o autor sugere mas não diz. Separe com clareza o que **o vídeo
diz** do que é **interpretação sua** (marque com *Interpretação:*).

## Citações marcantes
> "Frase exata da transcrição." — [12:34](url&t=754s)

## Afirmações verificáveis
| Afirmação | Tempo | Status | Fontes |
|---|---|---|---|
| "70% das pessoas..." | [05:10](url&t=310s) | ✅ Confirmada / ⚠️ Parcial / ❌ Contestada / ❓ Não verificada | [nome](url) |

## Conceitos e referências citados
Termos técnicos, livros, pessoas, estudos, ferramentas e empresas mencionados,
com uma linha explicando cada um e link quando fizer sentido.

## Contexto e pesquisa complementar
O que a pesquisa trouxe além do vídeo: visões contrárias, dados mais
recentes, quem mais fala do assunto.

## Aplicação prática
Ações concretas que dá para tirar do vídeo, quando houver.

## Perguntas em aberto
O que o vídeo não responde ou deixa ambíguo.

## Veredito
Vale assistir? Para quem? Qual a força e a fraqueza principal do conteúdo,
em 2 a 4 frases.

---

## Anexo: transcrição revisada

[00:00](url&t=0s) Texto revisado do primeiro bloco, em parágrafos...

[02:15](url&t=135s) Próximo bloco...

### Correções feitas
| Tempo | Original | Corrigido | Motivo |
|---|---|---|---|
| [03:12](url&t=192s) | "Daniel Caneman" | Daniel Kahneman | nome do autor citado; confirmado na descrição |

### Trechos incertos
- [08:40](url&t=520s): não ficou claro se ele diz "trinta" ou "treze" por cento
```

Corte as seções da análise que não fizerem sentido para o vídeo (uma entrevista casual pode não ter "Aplicação prática"). Não preencha seção com nada. O TL;DR, os Insights e o Anexo são obrigatórios.

Se a descrição do vídeo tiver capítulos, use-os como base da "Estrutura do vídeo" e confira os tempos no `.srt`.

#### Tempos e links

- Pegue o tempo do `.srt` (`00:12:34,500` vira `12:34`) e converta em link: `https://www.youtube.com/watch?v=<ID>&t=<segundos>s`.
- Cite o início do trecho, não o meio.
- Nunca invente tempo. Se não achar o trecho no `.srt`, cite sem tempo.

#### Pesquisa e checagem de fatos

Se você tem busca na web, use. Ela é o que diferencia a análise de um resumo.

1. **Escolha o que checar:** números e estatísticas, estudos citados, afirmações históricas, afirmações sobre saúde, dinheiro ou leis, e qualquer coisa que pareça exagerada. Opiniões e experiências pessoais não precisam de checagem.
2. **Priorize fontes primárias:** o estudo original, dados oficiais, documentação. Notícias e blogs vêm depois. Evite fontes que só repetem o vídeo.
3. **Cite tudo:** cada afirmação checada leva link e nome da fonte. Sem fonte, o status é ❓.
4. **Seja justo:** "parcialmente correta" é uma resposta válida. Diga o que está certo e o que está errado. Se a afirmação estava correta na época do vídeo mas mudou, explique.
5. **Pesquise o contexto:** quem é o palestrante, se tem conflito de interesse (vende o produto de que fala?), se há consenso ou controvérsia sobre o tema.

Se você **não** tem busca na web, diga isso no `.md`, marque as afirmações como ❓ e liste o que valeria checar.

#### Regras

- **Não invente.** Toda afirmação atribuída ao vídeo tem que estar na transcrição. Citação entre aspas é cópia exata (corrigindo só erro óbvio de transcrição).
- **Separe as vozes:** o que o vídeo diz, o que as fontes dizem e o que você conclui. O leitor precisa saber de onde veio cada coisa.
- **Não amenize nem exagere.** Se o vídeo tem erros graves, diga com fontes. Se é bom, diga também.
- Assuntos sensíveis (saúde, finanças, direito): aponte quando o vídeo der conselho sem base e indique fontes confiáveis.

### 1.5 Vários vídeos

Quando o usuário mandar vários links, gere um `.md` por vídeo. Se forem do mesmo tema, faça também `analises/comparativo-<tema>.md` com: onde os vídeos concordam, onde discordam (e quem tem mais apoio nas fontes), o que só um deles cobre e uma síntese final.

### 1.6 Ao terminar

No chat, entregue: o caminho do `.md`, o TL;DR, os 3 insights mais fortes e qualquer alerta importante (afirmação falsa, transcrição de baixa confiabilidade, vídeo cortado). Não cole o arquivo inteiro no chat.

---

## Parte 2: Desenvolver a ferramenta

Go 1.27+, sem dependências externas (só biblioteca padrão). Roda em Linux, macOS e Windows (amd64 e arm64).

| Arquivo | Responsabilidade |
|---|---|
| `main.go` | flags, subcomando `setup`, fila de vídeos, fluxo de cada vídeo |
| `media.go` | chamadas a `yt-dlp`, `ffmpeg` e `whisper-cli` |
| `setup.go` | localizar e baixar whisper e modelos (com SHA-256) |
| `vtt.go` | parser de WebVTT e geração de texto e SRT |
| `cpu_*.go` | núcleos físicos por sistema operacional |
| `util.go` | log, nomes de arquivo, utilidades |

Comandos:

```bash
make lint    # go vet + go fix -diff (precisa sair vazio)
make build   # binário estático em bin/
```

Antes de concluir qualquer mudança, rode `make lint build`, teste o comando de verdade com um vídeo e compile para os outros sistemas:

```bash
for t in linux/amd64 darwin/arm64 windows/amd64; do GOOS=${t%/*} GOARCH=${t#*/} go build -o /dev/null . || echo "falhou: $t"; done
```

Regras do código:

- **Multiplataforma sempre.** Use `filepath` (nunca `/` fixo em caminhos), `exeName()` para executáveis e `os.UserCacheDir()` para dados. Código específico de um sistema vai em arquivo `_linux.go`, `_darwin.go` ou `_windows.go`.
- **Processos externos** só por `command(ctx, ...)`: herda o cancelamento do Ctrl+C e limpa o `LD_LIBRARY_PATH` do Nix. Passe `--` antes de URLs vindas do usuário, para que não virem opções.
- **stdout é só para caminhos de arquivos gerados** (scripts e agentes dependem disso). Todo o resto vai para o stderr via `logger`.
- **Downloads** só via `fetch()`: grava em `.part`, confere o tamanho e o SHA-256 e só então renomeia. Binários baixados precisam de hash fixo no código.
- **Desempenho se mede, não se supõe.** Mudança que diz deixar mais rápido traz número: tempo do comando antes e depois. Mudança no whisper (flags, modelo) traz também a taxa de erro (WER) contra uma legenda manual.
- Mensagens para o usuário e comentários em português. Nomes no código em inglês.
- Sem dependências novas sem conversar antes.

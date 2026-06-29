# Relatório de Auditoria — Proxywi

**Data:** 2026-06-29  
**Escopo:** `proxywi-server`, `proxywi-client`, infraestrutura (Caddy/Docker), GUI web  
**Foco:** performance de banco de dados (SQLite), performance de processamento/network, segurança e privacidade  
**Metodologia:** análise estática do código-fonte e configurações por subagentes especializados em Go/SQLite, rede/cliente e Caddy/infra, com revisão cruzada manual dos trechos críticos.

---

## 1. Resumo Executivo

O Proxywi é um proxy pool “bring-your-own-IP” com arquitetura simples e bem modular: servidor Go monolítico, agentes que fazem túnel WSS+yamux, e Caddy na borda para TLS/L4. O projeto já adota boas práticas iniciais — WAL no SQLite, bcrypt para senhas/tokens, anonimização de IPs com HMAC, retenção curta de logs — mas apresenta gargalos sérios de escalabilidade e vários vetores de segurança/privacidade que devem ser tratados antes de uso em larga escala.

Os três riscos mais graves são:

1. **Autenticação de agente/token é O(N) em bcrypt** (`internal/server/control.go:159`, `internal/server/gui/token.go:13`). Cada handshake carrega todos os hashes de token e compara um a um.
2. **Agente pode ser usado como proxy aberto para redes internas** (`internal/client/client.go:133`). O cliente aceita qualquer `req.Target` do servidor, incluindo `localhost`, metadata cloud e RFC 1918.
3. **Tokens de cliente vazam em URL/query string** (`internal/server/gui/handlers.go:409`, `:434`), ficando em logs de acesso, histórico de navegador e headers `Referer`.

Além disso, há timeouts ausentes em conexões de proxy, falta de CSRF, cookies sem `Secure`, WebSocket aceitando qualquer origem, container do servidor rodando como root, e logs do Caddy expondo IPs reais.

---

## 2. Pontuação por Área

| Área | Nota (1-5) | Justificativa |
|------|-----------|---------------|
| Performance de banco (SQLite) | 2 | WAL e busy timeout presentes, mas `MaxOpenConns(1)`, índices insuficientes e token lookup O(N) limitam escala. |
| Performance de processamento/network | 2 | Goroutines sem limites, `pipe()` sem timeout, yamux em defaults, copy bidirecional prematuro no cliente. |
| Segurança | 2 | Bcrypt, rate-limit e IP hashing são positivos; porém CSRF ausente, cookies inseguros, WebSocket aberto, proxy aberto no cliente, root no container. |
| Privacidade | 2 | IPs anonimizados no banco, mas `target_host`/`username` logados, IP público do agente coletado de terceiros, logs Caddy com IPs reais. |
| Infraestrutura/Caddy/Docker | 2 | Caddy com TLS e PROXY protocol, mas sem headers de segurança, sem redirecionamento HTTP→HTTPS, container root, entrypoint frágil. |

---

## 3. Achados Detalhados

### 3.1 Performance de Banco de Dados SQLite

#### 🔴 CRÍTICO — Token lookup O(N) em bcrypt

- **Arquivos:** `internal/server/control.go:159-174`, `internal/server/gui/token.go:13-28`
- **Descrição:** `authenticateToken` e `clientByToken` carregam **todos** os `token_hash` da tabela `clients` e rodam `bcrypt.CompareHashAndPassword` em loop até encontrar correspondência. Com dezenas/centenas de clientes, cada handshake de agente ou login por token consome CPU e aumenta latência linearmente.
- **Recomendação:** armazenar um prefixo/identificador não-secreto junto ao hash (ex: `token_id` ou prefixo de 8 caracteres) e fazer `SELECT` indexado; ou usar uma tabela `client_tokens(id, client_id, token_hash)` com índice em uma coluna derivada. Manter bcrypt apenas na comparação final.
- **Esforço:** médio.

#### 🟠 ALTO — `SetMaxOpenConns(1)` serializa acessos

- **Arquivo:** `internal/storage/storage.go:41`
- **Descrição:** Apesar do WAL permitir leituras concorrentes, uma única conexão aberta força todas as queries (leitura do dashboard, inserção de eventos, métricas, autenticações) a disputarem o mesmo recurso.
- **Recomendação:** aumentar para 10-20 conexões, ajustar `SetMaxIdleConns` igual e monitorar `SQLITE_BUSY`. Manter busy_timeout em 5s.
- **Esforço:** baixo.

#### 🟠 ALTO — Índices insuficientes para queries de dashboard/logs

- **Arquivos:** `internal/storage/storage.go` (`CountProxyEventsFiltered`, `ListProxyEventsFiltered`, `OriginStats`)
- **Descrição:** O schema inicial (`migrations/000001_init.up.sql`) cria apenas índices em `ts`. Filtros por `user_id`, `client_id`, `source_ip` e `outcome` fazem full scans ou usam poucos índices.
- **Recomendação:** adicionar:
  ```sql
  CREATE INDEX idx_proxy_events_user_ts ON proxy_events(user_id, ts);
  CREATE INDEX idx_proxy_events_client_ts ON proxy_events(client_id, ts);
  CREATE INDEX idx_proxy_events_origin_ts ON proxy_events(source_ip, ts);
  CREATE INDEX idx_proxy_events_outcome_ts ON proxy_events(outcome, ts);
  ```
- **Esforço:** baixo.

#### 🟠 ALTO — Busca por `target_host` com `LIKE '%term%'`

- **Arquivo:** `internal/storage/storage.go:754-758`
- **Descrição:** O filtro de busca dos logs usa `e.target_host LIKE ?` com curinga nos dois lados, impedindo uso de índice e forçando full scan na janela de 24h.
- **Recomendação:** oferecer busca prefixada (`LIKE 'term%'`) com índice, ou usar FTS5 (`sqlite-fts5`) para busca textual eficiente.
- **Esforço:** médio.

#### 🟡 MÉDIO — Deleções em massa sem paginação

- **Arquivos:** `internal/storage/storage.go:655-678`, `:810-817`
- **Descrição:** `PurgeOldData` e `PurgeProxyEventsOlderThan` executam `DELETE` sem `LIMIT`. Em volumes grandes, isso segura locks do WAL e pode causar `SQLITE_BUSY` para escritas concorrentes.
- **Recomendação:** paginar deleções em loops com `DELETE ... LIMIT 1000` até zerar; rodar `VACUUM` periodicamente fora do horário de pico.
- **Esforço:** baixo.

#### 🟡 MÉDIO — `NormalizeLegacyClientNames` atualiza um a um

- **Arquivo:** `internal/storage/storage.go:258-289`
- **Descrição:** Para cada cliente legado, gera novo nome e executa `UPDATE` em transação separada.
- **Recomendação:** agrupar em uma única transação ou fazer bulk update.
- **Esforço:** baixo.

---

### 3.2 Performance de Processamento e Network

#### 🔴 CRÍTICO — `pipe()` sem timeout após handshake

- **Arquivo:** `internal/server/proxy_http.go:257-277` (também usado por `proxy_socks.go`)
- **Descrição:** Após o estabelecimento do túnel, não há `SetReadDeadline`/`SetWriteDeadline`. Conexões zumbis ficam presas em `io.Copy` indefinidamente, consumindo duas goroutines por fluxo.
- **Recomendação:** implementar `pipe` com deadlines periódicos (ex: 5 minutos de inatividade) e usar `CloseWrite` de forma segura; considerar `net.Conn` com contexto.
- **Esforço:** médio.

#### 🔴 CRÍTICO — Cliente fecha após primeiro `done`

- **Arquivo:** `internal/client/client.go:158-171`
- **Descrição:** A função `handleStream` retorna quando uma das direções de copy termina, fechando `stream` e `target` e potencialmente abortando dados em trânsito na outra direção.
- **Recomendação:** aguardar ambas as goroutines (`<-done` duas vezes) antes de fechar as conexões; usar `sync.WaitGroup`.
- **Esforço:** baixo.

#### 🟠 ALTO — SOCKS proxy sem limite de goroutines/conexões

- **Arquivo:** `internal/server/proxy_socks.go:45-53`
- **Descrição:** Cada conexão TCP aceita dispara `go s.handle(conn)` sem semáforo, `LimitListener` nem rate limit.
- **Recomendação:** usar `golang.org/x/net/netutil.LimitListener` ou semáforo para limitar conexões simultâneas.
- **Esforço:** baixo.

#### 🟠 ALTO — `http.Server` sem timeouts completos

- **Arquivo:** `cmd/proxywi-server/main.go:136-155`
- **Descrição:** Apenas `ReadHeaderTimeout` está configurado. Faltam `ReadTimeout`, `WriteTimeout`, `IdleTimeout`, `MaxHeaderBytes`.
- **Recomendação:** configurar todos os timeouts e `MaxHeaderBytes`; adicionar `MaxConnsPerIP` se possível.
- **Esforço:** baixo.

#### 🟠 ALTO — `readMetaStream` aceita apenas um stream meta e dispara goroutine de ticker

- **Arquivo:** `internal/server/control.go:125-157`
- **Descrição:** Após aceitar o primeiro stream meta, o servidor lê heartbeats. A goroutine do ticker (`MarkClientSeen`) só para quando `ctx` é cancelado; se o cancelamento não for imediato, há leak. Além disso, o servidor não responde aos heartbeats (unidirecional).
- **Recomendação:** garantir cancelamento do contexto ao fechar a conexão; implementar healthcheck bidirecional; limitar número de streams meta.
- **Esforço:** médio.

#### 🟡 MÉDIO — Configuração default do yamux

- **Arquivos:** `internal/server/control.go:82-85`, `internal/client/client.go:84-87`
- **Descrição:** `AcceptBacklog`, `MaxStreamWindowSize`, `ConnectionWriteTimeout` usam defaults. Em alta carga, backpressure pode aumentar latência ou estourar buffers.
- **Recomendação:** ajustar configurações com base em benchmarks; documentar limites.
- **Esforço:** baixo.

#### 🟡 MÉDIO — Proxy HTTP não-CONNECT reescreve resposta completa

- **Arquivo:** `internal/server/proxy_http.go:159-175`
- **Descrição:** Para requisições absolutas (não CONNECT), o servidor lê e reescreve a resposta HTTP inteira, mais lento que `io.Copy` direto e expõe o parser a payloads malformados.
- **Recomendação:** quando possível, fazer relay direto do body após copiar headers; tratar chunked encoding com cuidado.
- **Esforço:** médio.

#### 🟡 MÉDIO — Métricas de bytes no cliente são imprecisas

- **Arquivo:** `internal/client/client.go:199-201`
- **Descrição:** `copyCounted` usa `io.Copy` sem contar bytes retornados corretamente no path principal; `bytesOut`/`bytesIn` são incrementados manualmente apenas para dados buffered.
- **Recomendação:** usar `io.CopyBuffer` e retornar bytes copiados; usar `sync.Pool` de buffers.
- **Esforço:** baixo.

#### 🟢 BAIXO — Buffers default de `bufio.Reader`/`io.Copy`

- **Arquivos:** `internal/server/proxy_http.go`, `internal/server/proxy_socks.go`, `internal/client/client.go`
- **Descrição:** Buffers de 4 KiB (`bufio.NewReader`) e 32 KiB (`io.Copy`) podem ser insuficientes para alto throughput.
- **Recomendação:** testar com buffers maiores (ex: 64-256 KiB) e `io.CopyBuffer` com `sync.Pool`.
- **Esforço:** baixo.

---

### 3.3 Segurança

#### 🔴 CRÍTICO — WebSocket aceita qualquer origem

- **Arquivo:** `internal/server/control.go:33-35`
- **Descrição:** `InsecureSkipVerify: true` desabilita verificação de host/origin no WebSocket, permitindo que sites arbitrários abram conexão contra `/ws/control`.
- **Recomendação:** restringir origins permitidas (ex: validar `Origin` header contra `PROXYWI_DOMAIN`) ou separar listener de agentes da GUI.
- **Esforço:** baixo.

#### 🔴 CRÍTICO — Tokens de cliente expostos em URL

- **Arquivos:** `internal/server/gui/handlers.go:409`, `:434`
- **Descrição:** Após criar/regenerar um cliente, o servidor redireciona para `/clients?token=<token>&id=<id>`. O token real fica em query string, logs de acesso, histórico do navegador e `Referer`.
- **Recomendação:** usar sessão flash para exibir o token uma única vez na página destino; nunca colocar token em URL.
- **Esforço:** baixo.

#### 🔴 CRÍTICO — Agente pode acessar destinos arbitrários

- **Arquivo:** `internal/client/client.go:133-138`
- **Descrição:** O cliente faz `DialContext` para qualquer `req.Target` enviado pelo servidor, sem ACL. Isso permite acesso a `localhost`, redes internas (10/8, 172.16/12, 192.168/16), link-local (`169.254.0.0/16`), metadata cloud (`169.254.169.254`), portas sensíveis, etc.
- **Recomendação:** implementar ACL padrão-deny no agente, bloqueando RFC 1918, loopback, link-local, multicast e metadata; permitir override explícito via env.
- **Esforço:** médio.

#### 🟠 ALTO — Cookie de sessão sem flag `Secure`

- **Arquivo:** `internal/server/gui/middleware.go:70-78`
- **Descrição:** O cookie `proxywi_session` é `HttpOnly` e `SameSite=Lax`, mas não `Secure`. Se o GUI listener for acessado diretamente via HTTP, o cookie transita em claro.
- **Recomendação:** adicionar `Secure: true` quando TLS estiver ativo; usar `SameSite=Strict` para rotas admin.
- **Esforço:** baixo.

#### 🟠 ALTO — CSRF ausente em POSTs administrativos

- **Arquivos:** `internal/server/gui/handlers.go`, `internal/server/gui/templates/*.html`
- **Descrição:** Formulários de criação/remoção de clientes, usuários, bans e allowlist usam POST sem token CSRF. `SameSite=Lax` mitiga parcialmente, mas não protege totalmente.
- **Recomendação:** gerar token CSRF por sessão e validar em todo POST; incluir nos templates.
- **Esforço:** médio.

#### 🟠 ALTO — `postSetup` race condition

- **Arquivo:** `internal/server/gui/handlers.go:62-118`
- **Descrição:** `CountAdmins` e `CreateAdmin` não são atômicas. Requisições simultâneas podem criar múltiplos admins.
- **Recomendação:** usar transação com constraint única/índice `UNIQUE` em `admins.username` ou `email`.
- **Esforço:** baixo.

#### 🟠 ALTO — Mensagens de erro expondo detalhes do banco

- **Arquivo:** `internal/server/gui/handlers.go:102-103`, `:552`, `:580`, etc.
- **Descrição:** Várias rotas retornam `"db error: "+err.Error()` ao cliente, revelando constraints e detalhes internos.
- **Recomendação:** logar o erro no servidor e retornar mensagem genérica ao usuário.
- **Esforço:** baixo.

#### 🟠 ALTO — `Proxy-Authorization` Basic sem TLS obrigatório

- **Arquivos:** `internal/server/proxy_http.go`, `internal/server/proxy_socks.go`
- **Descrição:** Credenciais de proxy transitam em base64. O servidor não força TLS nos listeners de proxy; depende inteiramente do Caddy.
- **Recomendação:** documentar que proxy listeners nunca devem ser expostos sem TLS; considerar rejeitar conexões não-TLS se possível.
- **Esforço:** baixo.

#### 🟠 ALTO — PROXY protocol confiado cegamente

- **Arquivos:** `docker/server/Caddyfile:5-23`, `internal/config/config.go`
- **Descrição:** Caddy encaminha PROXY protocol v2 para `:8080`/`:11080`. Se essas portas internas vazarem para a rede, um atacante pode spoofar IP de origem. O backend aceita PROXY protocol quando `PROXYWI_PROXY_PROTOCOL=true`.
- **Recomendação:** garantir que `:8080`/`:11080` nunca sejam publicados; vincular PROXY protocol a interfaces de confiança; documentar risco.
- **Esforço:** baixo.

#### 🟡 MÉDIO — `stripHopByHopHeaders` não normaliza casing de `Connection`

- **Arquivo:** `internal/server/proxy_http.go:204-215`
- **Descrição:** `http.Header.Get` retorna apenas o primeiro header `Connection`; headers hop-by-hop extras podem permanecer no request upstream.
- **Recomendação:** iterar sobre todos os valores de `Connection` e normalizar casing.
- **Esforço:** baixo.

#### 🟡 MÉDIO — Headers de segurança HTTP ausentes no GUI

- **Arquivos:** `internal/server/gui/gui.go`, `internal/server/gui/handlers.go:844`, `docker/server/Caddyfile`
- **Descrição:** Faltam `Content-Security-Policy`, `X-Frame-Options`, `X-Content-Type-Options`, `Strict-Transport-Security` e `Permissions-Policy`.
- **Recomendação:** adicionar via middleware Go e/ou Caddy.
- **Esforço:** baixo.

#### 🟡 MÉDIO — Container do servidor roda como root

- **Arquivo:** `docker/server/Dockerfile`
- **Descrição:** Não há usuário não-root. Caddy e `proxywi-server` rodam como root.
- **Recomendação:** criar usuário dedicado, ajustar permissões de `/data`, usar `cap_net_bind_service` para Caddy ou publicar portas altas.
- **Esforço:** médio.

#### 🟡 MÉDIO — Entrypoint sem init system

- **Arquivo:** `docker/server/entrypoint.sh`
- **Descrição:** Shell script gerencia Caddy e `proxywi-server` manualmente; sem reaping de zumbis; falha de um processo mata o outro sem log claro.
- **Recomendação:** usar `tini`, `supervisord` ou `s6` como init.
- **Esforço:** baixo.

#### 🟢 BAIXO — `admin-set` CLI sem validação de e-mail/unicidade

- **Arquivo:** `cmd/proxywi-server/admin.go`
- **Descrição:** Permite alterar username/email sem validar formato ou unicidade.
- **Recomendação:** adicionar validação básica.
- **Esforço:** baixo.

---

### 3.4 Privacidade

#### 🔴 CRÍTICO — Token do cliente em URL (reiterado)

- **Impacto:** vincula identidade do cliente a logs de acesso, histórico e referrers.
- **Recomendação:** sessão flash.

#### 🟠 ALTO — `target_host` e `username` armazenados em `proxy_events`

- **Arquivos:** `internal/server/proxy_http.go:217-255`, `internal/server/proxy_socks.go:159-197`
- **Descrição:** Cada destino de navegação e o usuário proxy ficam registrados por até 24h. Mesmo anonimizado o IP, essa metadata é sensível.
- **Recomendação:** oferecer modo de retenção menor ou anonimização/agregação de destinos; documentar política de retenção.
- **Esforço:** médio.

#### 🟠 ALTO — Coleta de IP público do agente via terceiros

- **Arquivo:** `internal/client/client.go:208-236`
- **Descrição:** O agente consulta `ifconfig.me` e `icanhazip.com`, envia `User-Agent: curl/proxywi` (fingerprint) e repassa o IP ao servidor via `SelfReportedIP`.
- **Recomendação:** tornar opt-in (`PROXYWI_REPORT_PUBLIC_IP=false` por padrão); remover User-Agent identificável; não logar IP localmente.
- **Esforço:** baixo.

#### 🟡 MÉDIO — `auth_failures.username_attempted` guarda tentativas

- **Arquivo:** `internal/storage/storage.go:819-824`
- **Descrição:** Armazena o username tentado, que pode ser um dado pessoal ou até uma senha digitada no campo errado.
- **Recomendação:** avaliar necessidade de reter username; considerar hash ou exclusão.
- **Esforço:** baixo.

#### 🟡 MÉDIO — `ip_allowlist` em plaintext

- **Arquivo:** `internal/storage/storage.go:970-974`
- **Descrição:** Necessário funcionalmente, mas é uma tabela de IPs reais sem criptografia em repouso.
- **Recomendação:** proteger volume `/data` com criptografia de disco; documentar retenção.
- **Esforço:** médio.

#### 🟡 MÉDIO — Caddy logs podem conter IPs reais

- **Arquivo:** `docker/server/Caddyfile`
- **Descrição:** Sem configuração de log, Caddy grava IP de origem, host, URI e user-agent por padrão, contradizendo a anonimização no banco.
- **Recomendação:** desabilitar access logs ou anonimizar IPs no formato de log.
- **Esforço:** baixo.

#### 🟡 MÉDIO — SSE/Hub publica eventos para todos os subscribers

- **Arquivo:** `internal/server/hub.go`
- **Descrição:** Métricas e eventos de proxy são enviados a todos os admins conectados, sem filtragem por sessão/escopo.
- **Recomendação:** filtrar eventos de acordo com o admin logado (ex: clientes permitidos).
- **Esforço:** médio.

#### 🟢 BAIXO — `FilterQS` injetado como `template.HTML`

- **Arquivo:** `internal/server/gui/handlers.go:722`
- **Descrição:** Embora construído a partir de `url.Values.Encode()`, o uso de `template.HTML` é risco de XSS se alguma parte da query for reutilizada sem escape no futuro.
- **Recomendação:** remover `template.HTML` e escapar normalmente.
- **Esforço:** baixo.

---

## 4. Tabela de Recomendações Priorizadas

| # | Prioridade | Área | Recomendação | Arquivos | Esforço |
|---|-----------|------|--------------|----------|---------|
| 1 | 🔴 Crítico | DB/Seg | Trocar token lookup O(N) por lookup indexado | `storage.go`, `control.go`, `token.go` | Médio |
| 2 | 🔴 Crítico | Seg/Client | Implementar ACL de destinos no agente | `internal/client/client.go` | Médio |
| 3 | 🔴 Crítico | Seg/GUI | Remover token da query string; usar sessão flash | `handlers.go`, templates | Baixo |
| 4 | 🔴 Crítico | Network | Adicionar timeouts no `pipe()` de proxy | `proxy_http.go`, `proxy_socks.go` | Médio |
| 5 | 🔴 Crítico | Network | Corrigir copy bidirecional do cliente | `internal/client/client.go` | Baixo |
| 6 | 🟠 Alto | Seg | Restringir origens do WebSocket `/ws/control` | `control.go` | Baixo |
| 7 | 🟠 Alto | Seg | Adicionar CSRF a todos os POSTs administrativos | `middleware.go`, handlers, templates | Médio |
| 8 | 🟠 Alto | Seg | Hardening de cookies (`Secure`, `SameSite=Strict`) | `middleware.go` | Baixo |
| 9 | 🟠 Alto | DB | Aumentar `MaxOpenConns` e ajustar pool | `storage.go` | Baixo |
| 10 | 🟠 Alto | DB | Adicionar índices críticos em `proxy_events` | migrations | Baixo |
| 11 | 🟠 Alto | Network | Limitar conexões/goroutines no SOCKS proxy | `proxy_socks.go` | Baixo |
| 12 | 🟠 Alto | Network | Configurar timeouts completos no `http.Server` | `main.go` | Baixo |
| 13 | 🟠 Alto | Priv | Tornar coleta de IP público opt-in | `client.go`, `protocol.go` | Baixo |
| 14 | 🟠 Alto | Priv | Anonimizar/desabilitar logs de acesso do Caddy | `Caddyfile` | Baixo |
| 15 | 🟡 Médio | DB | Paginar deleções em massa | `storage.go` | Baixo |
| 16 | 🟡 Médio | DB | Busca de logs com FTS5 ou prefixo indexado | `storage.go` | Médio |
| 17 | 🟡 Médio | Seg | Adicionar headers de segurança HTTP | `gui.go`, `Caddyfile` | Baixo |
| 18 | 🟡 Médio | Seg | Corrigir race condition no setup inicial | `handlers.go` | Baixo |
| 19 | 🟡 Médio | Seg | Não expor `err.Error()` em respostas HTTP | vários handlers | Baixo |
| 20 | 🟡 Médio | Infra | Rodar servidor como non-root | `Dockerfile`, `entrypoint.sh` | Médio |
| 21 | 🟡 Médio | Infra | Usar init system no container servidor | `entrypoint.sh` | Baixo |
| 22 | 🟡 Médio | Priv | Revisar retenção de `target_host`/`username` | `proxy_http.go`, `proxy_socks.go` | Médio |
| 23 | 🟡 Médio | Priv | Filtrar eventos SSE por admin | `hub.go`, `sse.go` | Médio |
| 24 | 🟢 Baixo | Network | Tuning de buffers yamux/copy | vários | Baixo |
| 25 | 🟢 Baixo | Infra | Adicionar `HEALTHCHECK` ao Dockerfile | `Dockerfile` | Baixo |
| 26 | 🟢 Baixo | Seg | Validar `X-Forwarded-Host`/`X-Forwarded-Proto` | `handlers.go` | Baixo |

---

## 5. Roadmap Sugerido

### Sprint 1 — Segurança crítica (1-2 semanas)
- [ ] ACL de destinos no agente
- [ ] Token fora da URL
- [ ] Restringir origins do WebSocket
- [ ] CSRF + cookies hardened
- [ ] Timeouts no `pipe()`

### Sprint 2 — Escalabilidade do banco (1 semana)
- [ ] Índices críticos
- [ ] `MaxOpenConns` tuning
- [ ] Lookup de token indexado
- [ ] Paginação de deletes

### Sprint 3 — Hardening de infra e privacidade (1 semana)
- [ ] Container non-root + init system
- [ ] Caddy headers, redirecionamento HTTPS, logs anonimizados
- [ ] IP público opt-in
- [ ] Revisão de retenção de metadata

### Sprint 4 — Qualidade e monitoramento (contínuo)
- [ ] Adicionar testes automatizados (nenhum `*_test.go` existe hoje)
- [ ] Métricas de desempenho do SQLite
- [ ] Benchmarks de throughput do proxy

---

## 6. Conclusão

O Proxywi tem uma base sólida para um projeto de tamanho médio, mas **não está pronto para produção em escala ou para ambientes com requisitos rígidos de segurança/privacidade** sem as correções acima. Os itens **críticos** (token lookup O(N), proxy aberto no cliente, token em URL e timeouts ausentes) devem ser tratados imediatamente. Os itens de **alto impacto** (CSRF, cookies, índices, pool de conexões, WebSocket aberto, coleta de IP) devem seguir em seguida.

Recomenda-se aprovar a fase de remediação (Tasks 11–14 do plano) após revisão deste relatório.

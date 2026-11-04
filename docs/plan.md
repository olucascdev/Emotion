# Emotion — plano inicial do sistema

**Status:** proposta para aprovação; nenhuma aplicação foi implementada.  
**Objetivo:** substituir o livro caixa em papel de uma empresa de fotografia por um sistema simples para registrar serviços, agenda, dinheiro recebido, despesas e valores pendentes. A pergunta principal que a tela inicial deve responder é: **quanto entrou, quanto saiu e quanto sobrou neste mês?**

## 1. Escopo e princípios

- Uso interno de uma única empresa, por **1 ou 2 pessoas**. Sem cadastro público e sem recursos de múltiplas empresas.
- Português do Brasil, valores em reais (R$) e calendário em `America/Sao_Paulo` (horário de Brasília). Usar o identificador de fuso, não um deslocamento `-03:00` fixo.
- Priorizar lançamento rápido, leitura fácil e poucos cliques. Um serviço simples, como foto 3×4 paga na hora, deve poder ser registrado em uma tela.
- Separar **venda/valor previsto** de **dinheiro efetivamente recebido**. Uma OS de R$ 1.200 parcelada em 12 vezes não é uma entrada de R$ 1.200 no caixa no dia da venda.
- O resultado mensal exibido no início será **recebimentos do mês − despesas pagas no mês** (regime de caixa). Chamá-lo de “resultado do mês”; lucro contábil ou fiscal exige regras adicionais e não será prometido pelo sistema.
- Manter histórico de alterações relevantes, especialmente pagamentos e cancelamentos. Corrigir registros sem apagar a trilha financeira.

## 2. Navegação e telas

Sidebar com três opções principais e textos grandes: **Início e Financeiro**, **Ordens de serviço** e **Agenda**. No rodapé, **Configurações** para o administrador, sem virar uma quarta área de trabalho do dia a dia.

### 2.1 Início e Financeiro

**Visão inicial:** período selecionável (hoje, este mês ou intervalo), com cartões de:

1. **Entradas recebidas** — pagamentos registrados no período.
2. **Despesas pagas** — saídas registradas no período.
3. **Resultado do mês** — entradas recebidas menos despesas pagas.
4. **A receber** — parcelas ainda não recebidas; destacar vencidas.
5. **A pagar** — despesas ainda não pagas; destacar vencidas.

Abaixo dos cartões: gráfico simples de entradas e saídas por dia ou mês; lista cronológica do **livro caixa**; próximos vencimentos; filtros por período, categoria, forma de pagamento e OS. Cada linha do livro caixa mostra data, descrição, origem, forma de pagamento, entrada/saída e valor. Permitir exportar CSV para conferência.

**Ações rápidas:** “Registrar entrada”, “Registrar despesa”, “Criar OS” e “Marcar pagamento”. Entradas avulsas atendem receitas que não vieram de OS. O sistema deve impedir contabilizar duas vezes uma entrada vinculada à mesma OS.

**Despesas e contas a pagar:** descrição, categoria, valor, vencimento, data de pagamento, forma de pagamento, observação e estado (pendente, parcial, paga, vencida ou cancelada). Permitir registrar despesa já paga ou a pagar. Pagamento parcial gera saldo pendente. Categorias iniciais: aluguel, energia, internet, materiais, transporte, taxas e outros; o administrador pode ajustá-las.

### 2.2 Ordens de serviço (OS)

Lista pesquisável por número, cliente, serviço, período e situação. Ações principais: **Nova OS**, **Registrar pagamento**, **Concluir** e **Cancelar**. Situações operacionais: rascunho, aberta, em andamento, concluída e cancelada. A situação financeira aparece separada: não paga, parcial ou quitada.

**Campos da OS:** número sequencial; data de criação; cliente (nome e telefone opcionais para venda rápida); tipo de serviço (ex.: foto 3×4, ensaio, casamento, evento, impressão, caneca, camiseta, almofada, porta-retrato ou outro); descrição; itens com quantidade e preço; desconto; valor total; data do serviço; horário inicial e final; local; data prevista de entrega; observações; vínculo opcional com compromisso da agenda; usuário que criou/alterou. Ensaios, casamentos e eventos criam automaticamente um compromisso vinculado; produtos e foto 3×4 não ocupam a agenda.

**Pagamento da OS:** valor de entrada/sinal, forma(s) de pagamento, quantidade de parcelas, valores e vencimentos de cada parcela, pagamentos efetivos, saldo restante. Formas iniciais: dinheiro, Pix, cartão de débito, cartão de crédito e transferência; “fiado” é condição de **valor a receber**, não uma entrada no caixa. Permitir combinações, como sinal por Pix e restante parcelado. Registrar taxas de cartão como despesa separada quando ocorrerem, para não esconder o custo.

Exemplos que devem funcionar:

| Combinação | Registro esperado |
| --- | --- |
| Foto 3×4 de R$ 40 paga na hora | OS concluída ou aberta; R$ 40 recebidos; entrada de R$ 40 no livro caixa. |
| Ensaio de R$ 600 com 50% de sinal | R$ 300 recebidos agora; R$ 300 a receber, com vencimento definido. |
| Casamento de R$ 1.200 em 12 parcelas | 12 parcelas previstas de R$ 100; cada uma só entra no caixa quando o recebimento for confirmado. |
| Serviço de R$ 200 fiado | R$ 200 a receber; nenhuma entrada no livro caixa até o pagamento. |

O parcelamento do cliente e o calendário real de repasses da operadora de cartão podem ser diferentes. Na primeira versão, o usuário informa **a data e o valor realmente recebidos**; não há conciliação automática com operadoras.

### 2.3 Agenda

- Visualizações **diária, semanal e mensal**, com alternância visível no topo.
- Criar, editar e cancelar compromissos com título, início, fim, local, observação, situação e vínculo opcional com OS/cliente.
- Mostrar serviços e compromissos na agenda, mas não confundir vencimento financeiro com horário de ensaio. Vencimentos ficam no Financeiro e podem aparecer como aviso, não como compromisso.
- Destacar o dia atual, sinalizar conflitos de horário e oferecer navegação simples entre datas.
- Exibir horários em `America/Sao_Paulo`; armazenar instantes em UTC e datas de vencimento como datas locais, sem conversão indevida de dia.

## 3. Experiência de uso

- Interface responsiva para computador e celular, com sidebar persistente no desktop e menu simples no celular.
- Fonte **Inter**, tamanho confortável, bom contraste, botões grandes, rótulos explícitos e formulários curtos. Evitar depender apenas de cor para mostrar vencimento ou estado.
- Na criação de OS, usar dois blocos claros: **Serviço** e **Pagamento**. Mostrar sempre “Total”, “Já recebido” e “Falta receber” antes de salvar.
- Confirmar cancelamentos e alterações em pagamentos; mostrar mensagens claras de sucesso ou erro.
- Alternância **☀️ claro / 🌙 escuro** no topo, com preferência salva por usuário e respeito à preferência do sistema no primeiro acesso.
- Usar termos familiares (“Entrou”, “Saiu”, “Falta receber”) e ajuda curta ao lado de campos menos óbvios.

## 4. Usuários, login e segurança

- **Administrador:** acesso completo, incluindo usuários, categorias, configurações, exportação e histórico.
- **Operador/gerente:** cria e atualiza OS, agenda, despesas e recebimentos; consulta o financeiro; não altera usuários nem configurações de segurança. O administrador também pode operar o sistema.
- Criação inicial do administrador por comando local seguro na VPS; demais usuários são convidados/criados pelo administrador. Sem senha padrão, cadastro público ou recuperação por pergunta secreta.
- Senhas com hash **Argon2id**, parâmetros adequados à VPS e salt individual. Limite de tentativas de login por IP e conta, respostas genéricas e registro de eventos de autenticação.
- **JWT de acesso** assinado, de curta duração (proposta: 15 minutos), com verificação de algoritmo permitido, assinatura, expiração, emissor e público. Sessão renovável com token de atualização aleatório, rotativo e armazenado no banco apenas por hash; revogação no logout, troca de senha ou desativação da conta.
- Tokens em cookies `HttpOnly`, `Secure` e `SameSite` adequados ao fluxo; nunca em `localStorage`. Proteger operações de escrita contra CSRF e verificar autorização no backend em cada rota. Limitar o JWT a identificadores e permissões, sem dados financeiros ou pessoais.
- HTTPS obrigatório, cabeçalhos de segurança, consultas SQL parametrizadas, validação de entradas, segredos fora do repositório e logs sem senhas/tokens/dados sensíveis. O sistema não armazena números de cartão nem processa pagamentos.
- Registrar em auditoria quem criou, alterou ou cancelou OS, despesas e pagamentos, com data e valores anterior/novo quando aplicável.

## 5. Tecnologia e implantação

| Camada | Escolha inicial | Motivo |
| --- | --- | --- |
| Backend | Go, API HTTP no mesmo domínio do frontend | Implantação enxuta e regras financeiras centralizadas. |
| Frontend | React + TypeScript + Tailwind CSS | Componentes simples, formulários tipados e tema claro/escuro. |
| Banco | SQLite em arquivo persistente na VPS | Volume e concorrência baixos para 1–2 usuários; evita manter PostgreSQL. |
| Publicação | Uma VPS, proxy reverso com HTTPS e serviço Go | Uma instalação simples, sem serviços externos obrigatórios. |

O banco será usado com chaves estrangeiras, transações para OS e pagamentos, migrações versionadas e modo WAL quando adequado. Valores monetários serão guardados em **centavos inteiros**, nunca em ponto flutuante. Backups automáticos diários devem usar o mecanismo de backup consistente do SQLite, ser criptografados e enviados para fora da VPS; testar a restauração periodicamente. Acesso administrativo à VPS por chave SSH e atualizações regulares do sistema fazem parte da implantação.

## 6. Estrutura lógica dos dados

| Entidade | Responsabilidade principal |
| --- | --- |
| `users`, `sessions` | Contas, papéis e sessões revogáveis. |
| `customers` | Nome e contato; OS rápida pode usar cliente avulso sem cadastro permanente. |
| `services`, `service_order_items` | Tipos de serviço e itens/preços de cada OS. |
| `service_orders` | Número, cliente, estado, total, desconto, entrega e observações. |
| `receivables` | Parcelas previstas, vencimento e saldo a receber de cada OS. |
| `payments` | Recebimentos efetivos, data, valor, método e parcela/OS vinculada. |
| `expenses`, `expense_payments` | Contas a pagar e saídas efetivamente pagas, inclusive parciais. |
| `cash_entries` | Entradas avulsas e trilha consolidada do livro caixa, com referência à origem para evitar duplicidade. |
| `appointments` | Compromissos e vínculo opcional com OS. |
| `audit_events` | Histórico de ações financeiras e administrativas. |

As operações de criação de OS, geração de parcelas e registro de pagamentos devem ser transacionais. A soma das parcelas precisa fechar o valor a receber, inclusive quando a divisão exigir centavos residuais. Pagamentos não podem exceder o saldo sem uma confirmação e regra explícita. Cancelar OS com dinheiro recebido exige registrar estorno ou manter o recebimento e documentar o motivo; nunca simplesmente apagar a entrada.

## 7. Entrega em etapas

1. **Base:** projeto Go e frontend, SQLite/migrações, login, dois papéis, layout acessível e tema claro/escuro.
2. **Livro caixa:** entradas avulsas, despesas, pagamentos parciais, cartões de resumo, filtros e exportação CSV.
3. **OS:** cadastro rápido e completo, parcelas, sinal, fiado, recebimentos e vínculo com livro caixa.
4. **Agenda:** visões diária/semanal/mensal, compromissos e vínculo com OS.
5. **Operação na VPS:** HTTPS, backups externos, restauração testada, logs, auditoria e revisão de segurança antes do uso real.

### Critérios de aceite da primeira versão

- A pessoa consegue registrar uma foto 3×4 paga na hora sem navegar por várias telas; o valor aparece uma única vez nas entradas do dia.
- Consegue lançar uma despesa paga e outra pendente; apenas a paga reduz o resultado do mês, e a pendente aparece em “A pagar”.
- Consegue registrar uma OS com 50% de sinal, uma fiada e uma em 12 parcelas; os cartões “Recebido” e “A receber” mostram os valores corretos.
- Consegue ver a agenda por dia, semana e mês com horários corretos de São Paulo.
- O administrador consegue criar/desativar o segundo usuário e consultar quem alterou um pagamento.
- O backup da VPS é restaurado com sucesso em ambiente de teste antes de colocar dados reais.

## 8. Decisões para aprovar

Estas são as escolhas propostas para começar. Se alguma não corresponder à rotina da empresa, ajustamos antes de implementar:

1. Nome do projeto **Emotion**.
2. Três áreas principais: **Início e Financeiro**, **Ordens de serviço**, **Agenda**.
3. Relatório principal em **regime de caixa**; valores previstos aparecem separadamente.
4. Registro manual de pagamentos e repasses; **sem integração bancária ou com maquininhas** na primeira versão.
5. Um único negócio, no máximo dois usuários, SQLite na VPS e backup externo diário.
6. Sem emissão de nota fiscal, cobrança automática, WhatsApp automático ou armazenamento de arquivos de fotos na primeira versão.

## Referências técnicas consultadas

- [OWASP — Password Storage Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html)
- [OWASP — Session Management Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)
- [OWASP — JSON Web Token Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/JSON_Web_Token_Cheat_Sheet.html)
- [SQLite — Online Backup API](https://www.sqlite.org/backup.html)
- [SQLite — Write-Ahead Logging](https://www.sqlite.org/wal.html)

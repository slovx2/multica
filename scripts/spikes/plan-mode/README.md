# Native plan-mode protocol spike — QORA-5

2026-10-02 实测完成。**异步提问 → 结束回合 → 同会话下一轮续接可行；建 issue 那轮应退出 plan；Claude 计划正文需要处理空值和旧快照。** 本目录只有独立测试脚本、协议节选与报告，没有功能实现，不提 PR。

## 运行与范围

基线 `2ea01ae4e`（fork main，已包含 v0.6.1）。本机 Claude Code **2.1.282**，实际模型 `claude-opus-5-5[1m]`；Codex **0.157.1**，实际模型 `gpt-6-astra`。沿用机器现有认证及模型配置。每回合新起 CLI，Claude 用 `--resume`，Codex 用新 app-server + `thread/resume`，比只在常驻进程中续接更接近守护进程。

```bash
MULTICA_RUN_REAL_AGENT_SMOKE=1 python3 scripts/spikes/plan-mode/probe.py claude --out ../claude-primary
MULTICA_RUN_REAL_AGENT_SMOKE=1 python3 scripts/spikes/plan-mode/probe.py claude --supplemental --out ../claude-extra
MULTICA_RUN_REAL_AGENT_SMOKE=1 python3 scripts/spikes/plan-mode/probe.py codex --out ../codex-primary
MULTICA_RUN_REAL_AGENT_SMOKE=1 python3 scripts/spikes/plan-mode/probe.py codex --supplemental --out ../codex-extra
codex app-server generate-json-schema --experimental --out ../codex-schema
```

Python 3.11+，无第三方依赖；必须显式设置 smoke 开关才访问真实 CLI/账户。输出目录须为空；原始日志默认保留在私有目录，请勿直接提交。脚本输出 fixture 位置，保留文件供检查；所有子进程在返回前回收，没有常驻服务。可用 `--claude-model` / `--codex-model` 显式选择模型。

Claude 启动核心参数与 daemon 一致：`-p --output-format stream-json --input-format stream-json --verbose`，实验改为 `--permission-mode plan --permission-prompt-tool stdio`，移除禁用 AskUserQuestion，限制最多 10 个模型回合并使用空 MCP 配置。Codex 使用 stdio app-server、`experimentalApi:true`，禁用无关 MCP；明确设置 `approvalPolicy:never` / `sandbox:danger-full-access`，保持原生 collaboration instructions，未覆盖 `developer_instructions`。

建 issue 写入探针使用 fixture 内的 **`./bin/multica` 假 CLI**，只写 `mock-issues.jsonl`、不连服务器。真实 CLI 只跑 `multica --version`，发现本机安装的是 0.6.0；这不改变 v0.6.1 代码基线，亦未据此声称真实平台 create/update 已验证。没有创建测试 issue、部署或修改产品代码。

`evidence/*.jsonl` 是逐帧白名单节选：保留请求、回复、工具和终态；排除认证、启动配置和推理内容，替换机器绝对路径。Plan delta 只保留头尾样本，同时记录全量拼接校验。`export_evidence.py` 可重复导出，但不是通用脱敏器，仍须人工审查。编号 C01/C02 等对应 `claude-01-*`；X01/X02 等对应 `codex-01-*`。

## 1. Claude

**1a — ExitPlanMode、deny、计划正文**

【事实】C02/C05/C09 都收到 `control_request` → `request.subtype:can_use_tool`、`tool_name:ExitPlanMode`、`requires_user_interaction:true`。回复如下后，都获得 `result.subtype:success`、`is_error:false`、`terminal_reason:completed`，CLI 退出 0，没有实施计划：

```json
{"type":"control_response","response":{"subtype":"success","request_id":"<request-id>","response":{"behavior":"deny","message":"不批准。计划已提交用户审阅；现在结束回合，不实施、不重复调用 ExitPlanMode；决定将在下一条消息给出。"}}}
```

上面是等价缩写；实际英文/中文消息保存在日志。**deny 不等于协议级强制终止**：模型收到错误工具结果，再按消息要求输出等待语，正常结束。

正文有三个实测情况：

| 日志 | 权限请求 `request.input` | 文件/时序证据 |
| --- | --- | --- |
| C02 | `{}` | Write 与 ExitPlanMode 在写入结果前均已发出；完整正文在 Write 的 `input.content`，成功结果提供路径 |
| C05 | 有 `plan`、`planFilePath`，但 `plan` 是旧正文 | Edit 增加 `--quiet`，成功结果在权限请求前到达；请求的 `plan` 没有 `--quiet`，文件已有。见 `fixture-snapshots.json` |
| C09 | 有 `plan`、`planFilePath`，正文与文件一致 | 强调先等待 Write 成功，再单独调用 ExitPlanMode；在权限请求时读取文件，内容一致 |

【结论】可截取并拒绝批准。通常读取 `request.input.plan` / `planFilePath`，**不能把这两个字段视为必有且最新**。建议提示串行写计划后再提交，同时跟踪成功的计划文件 Write/Edit；在待处理文件操作完成后，从已识别的计划路径读取最终内容作卡片快照。空正文/无法确定路径时应明确失败或要求补交，不能拿最后一句“等待审批”充当计划。读文件候选路径须校验并限定于该会话的计划文件；这只是实现建议，本阶段没有实现提取器。

**1b — AskUserQuestion 与拒绝结束**

【事实】C01 的 `system/init.tools` 包含 AskUserQuestion；C07 去掉 `--permission-prompt-tool stdio` 后，AskUserQuestion 和 ExitPlanMode 都不在工具列表中。C01 权限请求：

```json
{"request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion","input":{"questions":[{"question":"问候 CLI 应该使用哪种颜色？","header":"Color","options":[{"label":"Blue","description":"用蓝色输出问候语（ANSI 蓝色，观感平和、冷静）"},{"label":"Red","description":"用红色输出问候语（ANSI 红色，醒目、强调感强）"}],"multiSelect":false}]},"tool_use_id":"toolu_017zXMihcbrb74KEsJN4SzCW","requires_user_interaction":true}}
```

`behavior:deny` 附“回答将在下一条消息给出、不要猜测、立即结束本轮”后，C01 在 2 个模型回合内正常成功结束，回复“正在等你选择颜色（Blue 或 Red）”。C11 捕获了 `multiSelect:true` 和 3 个选项，同样正常结束。

【结论】问题在 **`request.input.questions`**，不是 `tool_input`。仅移除 disallowedTools 不够，还需 stdio prompt flag。可用 deny 实现异步卡片；结束依赖模型遵守停止指令，生产适配器应有超时/中断兜底。结果与 [上游 #8048 实测评论](https://github.com/multica-ai/multica/issues/8048)一致。

**1c — resume 接回答/同意**

【事实】C01–C06 始终为 session `6c56da91-d7dc-430d-b8b5-2014a0a3bbd0`。C02 用 `--resume` 输入“Blue”，提交的计划保留 Blue 与最初的标记 ORCHID-73；C03 输入“计划已同意，仅用于起草 issue”，仍记住两者且没有实施。

【结论】进程退出后续接成立；回答是新的 user message，不是向已关闭 stdin 补发旧 control_response。收到“同意”不会自动克服 plan 模式的写入限制。

**1d — plan 下执行 multica**

【事实】C03 原生 plan 直接执行 `multica --version`，退出 0，无权限回调。为单独验证 host 批准路径，C08 增加 `--settings '{"permissions":{"ask":["Bash"]}}'`，收到 Bash 的 `can_use_tool` 后回复 `{"behavior":"allow","updatedInput":<原input>}`，同一命令成功。C06 要求执行会写本地记录的 `./bin/multica issue create --title PLAN_SPIKE_ONLY`，模型在发起 Bash 前拒绝；没有权限请求，mock 文件不存在。C10 切 `bypassPermissions` 后，同一 mock 命令成功。

【结论】**只读命令和 can_use_tool 自动批准均可行；不能推导出 issue 写操作可行。** 本次写操作被原生 plan 指令阻止，自动批准无请求可批。建 issue 那轮建议退出 plan。未测试真实服务端 create/update，不能把 mock 结果描述成平台集成成功。

## 2. Codex

**2a — plan item / delta / 自然结束**

【事实】本机生成 schema 及实际请求支持：

```json
{"method":"turn/start","params":{"threadId":"<thread-id>","input":[{"type":"text","text":"<prompt>"}],"collaborationMode":{"mode":"plan","settings":{"model":"gpt-6-astra"}}}}
```

实际事件顺序（ID 缩写）：

```json
{"method":"item/started","params":{"item":{"type":"plan","id":"p1","text":""},"threadId":"t1","turnId":"r1"}}
{"method":"item/plan/delta","params":{"threadId":"t1","turnId":"r1","itemId":"p1","delta":"#"}}
{"method":"item/completed","params":{"item":{"type":"plan","id":"p1","text":"# Hello CLI implementation plan\n..."},"threadId":"t1","turnId":"r1"}}
{"method":"turn/completed","params":{"threadId":"t1","turn":{"id":"r1","status":"completed","error":null}}}
```

X01、X03、X06 分别收到 192、288、332 个 delta，按到达顺序拼接均与 completed plan.text 完全一致；三轮均自然 completed，无需 interrupt。X04 只请求版本信息时返回普通 agentMessage，没有 plan item。

【结论】按 `(threadId,turnId,itemId)` 累积 delta，用 completed item 的 `text` 定稿；真正结束以 **`turn/completed`** 为准，不能在首个 plan item 或首个 delta 到达时截断。不是每一轮 plan mode 都产生计划卡片，也不要混淆内部工作清单 `turn/plan/updated` 与最终 `type:plan`。

**2b — 提问截取、如何结束、下一轮续接**

【事实】真正服务端请求是 **`item/tool/requestUserInput`**，不是 MCP elicitation。X02 的请求节选：

```json
{"id":0,"method":"item/tool/requestUserInput","params":{"threadId":"<thread-id>","turnId":"<turn-id>","itemId":"<tool-call-id>","questions":[{"id":"output_style","header":"Style","question":"Which output style do you prefer?","isOther":true,"isSecret":false,"options":[{"label":"Compact","description":"One line."},{"label":"Verbose","description":"Extra detail."}]}],"isBlocking":true,"autoResolutionMs":null}}
```

三条路径均有实测：

| 路径 | 实际操作与终态 | 续接 |
| --- | --- | --- |
| 不答，直接中断 | X02 发 `turn/interrupt {threadId,turnId}`；收到 RPC 成功和 `turn/completed.status:interrupted`。X11 留请求悬挂 3 秒无 completed，再中断成功 | 关闭进程、`thread/resume`，X03 新输入 Verbose，生成修订计划且记住 Blue / ORCHID-73；X12 还验证了恢复后直接 default 写入 Red / IRIS-92 |
| 返回真实答案 | X07 对服务端 request id 回复 `{"answers":{"tests":{"answers":["Unit"]}}}`，模型继续回复 Selected Unit，completed | X09 仍记得 Unit |
| 返回空答案 | X08 回复 `{"answers":{}}`，**且本轮 prompt 明确要求空答案时停止、不得猜测**，模型输出等待语，completed | X09 新输入 Chinese，能接上所有已有选择 |

【结论】建议保存问题卡片后 **直接 turn/interrupt**，无需伪造答案；等 interrupted 终态确认，再释放进程。下一轮发送结构化问题上下文与人类回答、恢复同一 thread。把这种预期 interrupted 映射为产品层“待回答”，不要当运行失败或触发自动重试。真实答案会让模型继续；空答案不具有协议级停止保证，本次成功依赖提示词，不宜作为强制结束机制。后续版本还应按 `isBlocking` 分流，不能假定所有提问都阻塞。

**2c — plan 下 shell / multica**

【事实】X04 `commandExecution.command` 为 `/bin/bash -lc 'multica --version'`，`exitCode:0`。X10 要求执行写入 mock issue 的命令，模型回复“writes to mock-issues.jsonl ... mutation”，没有发起 commandExecution；mock 文件不存在。X12 在 default 模式运行同一命令成功并生成本地记录。

【结论】plan 允许只读 shell，不支持依靠用户“计划已批准”覆盖原生禁止写入指令。与 Claude 一致，建 issue 那轮建议切 default；这是观察到的模型行为，不是对 plan 沙箱绝对安全性的证明。

**2d — 同 thread 下一轮切 default**

【事实】X01–X10 始终为 thread `01a0fbce-2f3e-7d62-88b3-e0e83cc3d034`。X05 只把 `collaborationMode.mode` 改为 `default`，恢复后写出 `implemented.txt`，包含 Blue、Verbose、ORCHID-73，completed；X06 切回 plan，读取该文件并生成含 `--quiet` 的修订计划，没有实施。

【结论】同 thread 跨进程切换正常。每轮显式传 mode，避免依赖持久状态推断。现有 `codex.go` 未处理 requestUserInput，需新增分支；无需新增服务端到 daemon 的实时回答通道。

## 3. 来回切模式

【事实】Claude 主测试覆盖 plan → manual → plan；补测 C09→C10→C11 覆盖守护进程实际使用的 **plan → bypassPermissions → plan**，同 session `7cfc3431-4418-4b5f-bd8f-fa691f8acb20`，普通轮确实写文件/执行 mock，切回后读取文件并重新提出多选问题。测试机器现有 `IS_SANDBOX=1`，脚本未为绕过 root 检查修改它。Codex X04→X05→X06 覆盖 plan → default → plan；X11→X12→X13 还覆盖“中断问题 → 恢复 default → 恢复 plan 再提问”。

【结论】两家都通过，状态和前文记忆保留。Claude 的非计划权限模式名称不是 Codex 的 `default`：本版本普通交互参数为 `manual`，Multica 当前执行参数为 `bypassPermissions`；实现时不可直接共用 CLI 枚举。

## 4. 统一提问卡片草案

【事实】Claude 问题无稳定 id，有 `multiSelect`；Codex 有 `id`、`isOther`、`isSecret`、nullable `options`，没有 multiSelect。Claude 已实测单选/多选；Codex 实测单选及返回答案。无选项/secret 的分支仅由本机 schema 确认，未做 UI 实测。

【结论】卡片自身生成稳定问题/选项 id，原始 provider 标识另存；wire 使用 snake_case。完整示例见 `question-card.example.json`，核心结构为：

```json
{
  "schema_version": 1,
  "id": "card-id",
  "kind": "user_question",
  "title": "需要确认",
  "status": "pending",
  "continuation": "new_turn",
  "source": {"provider":"codex","conversation_id":"thread-id","turn_id":"turn-id","item_id":"call-id","request_id":0,"is_blocking":true},
  "questions": [{
    "id":"q0","native_id":"output_style","native_index":0,
    "header":"Style","text":"Which output style do you prefer?",
    "selection_mode":"single",
    "options":[{"id":"o0","label":"Compact","description":"One line."},{"id":"o1","label":"Verbose","description":"Extra detail."}],
    "free_text":{"allowed":true,"secret":false},"required":true
  }],
  "answers": null
}
```

提交后 `answers` 可为 `[{"question_id":"q0","selected_option_ids":["o1"],"text":null}]`。Claude：conversation_id=session_id、item_id=tool_use_id、turn_id/native_id 可空，multiSelect 映射 single/multiple，保留 index 和原问题文本。Codex：保留原 question.id，isOther 映射自由输入，isSecret 映射敏感输入；options 为空时 selection_mode=none。Claude 自由输入设为产品默认允许；title、required 是产品设计字段，不冒充原生字段。不要拿 label/问题文字当卡片主键，也不要把旧 request_id 用作下一轮回答目标。secret 内容不能进入普通日志。

## 对父 issue 的影响与建议

1. 保留“提问结束回合、下一轮恢复”的架构，无需新实时通道；卡片持久化成功后再 deny/interrupt，并按 call/item id 去重，处理保存失败和重试。
2. **同意并建 issue 那轮，两家均退出 plan**，同步前端标签；新输入明确只 create/update issue、不实施。这是父方案已有的降级分支，但只读 `--version` 成功不足以决定继续保持 plan。真实 issue create/update 集成仍留待实现阶段。
3. **Claude 计划提取要新增空正文/旧快照处理**。提示串行写计划只是降低概率，不能代替文件完成状态与正文校验；测试必须覆盖本次 `{}` 和 stale plan。
4. Codex 等 terminal event；把问题中断视为预期待回答，不记作失败。不要用默认选项/空答案冒充用户决定。
5. deny 是模型级停止建议，interrupt 是明确的回合控制。后续集成测试应覆盖模型重试提问、超时、重启、重复投递及用户驳回后重新提交计划。

这些是阶段 1 结论与建议，**尚未变更父 issue 的已确认方案，也未开始阶段 2**；请 Mika 检查，并按父 issue 约定由 Kal 确认方案调整后再放行。

验证：24 个真实回合，Claude 11 个 success；Codex 10 个 completed、3 个预期 interrupted；24 个 CLI 子进程均退出 0。核对同会话标识、真实 fixture 文件、3 组 delta 全量拼接、串行计划正文一致性。脚本语法、JSON 解析、smoke 开关及 `git diff --check` 通过。没有改产品代码，因此未运行全仓 Go/前端测试。结果是当前本机版本/模型的观察，不是跨版本协议承诺。

参考：[OpenAI 官方 App Server 文档](https://learn.chatgpt.com/docs/app-server)说明 stdio 生命周期与 completed/failed/interrupted 终态；本报告字段及行为以本机生成的 `schema-excerpt.json` 和实际事件为准。

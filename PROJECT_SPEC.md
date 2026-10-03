# 泄洪闸门操作协调服务

## 项目目标

该服务用于水工控制室协调泄洪闸门的命令申请、安全联锁复核和单次开度调整。系统在同一闸门上只允许一条已通过复核且尚未结束的执行命令，任何冲突都必须被显式拒绝，不能覆盖已经占用的闸门。

跨上下游的联动调整通过操作计划（operation plan）按业务顺序推进。计划登记只保存各阶段意图，不提前创建命令或占用闸门；每个阶段必须在上一阶段命令真实完成、且当前闸门条件重新确认后才能启动。前置失败、并发占用、条件变化或重复推进都会得到稳定的冲突解释；已完成阶段不会被后续阻塞回写，未开始阶段也不会提前占用闸门。

## 使用角色

- 运行协调员：登记闸门、提交开度调整命令、登记联动计划并推进阶段。
- 安全复核员：提交联锁结论，决定命令是否通过。
- 闸门操作员：凭执行令牌启动命令并登记执行结果。

## 核心实体

- `gate`：闸门及其当前开度、运行状态、最大开度和活动命令。
- `gate_command`：一次开度调整申请，包含目标开度、申请原因、状态和执行令牌；可通过 `plan_id` 与阶段序号绑定到联动计划。
- `interlock_decision`：安全复核结论，状态为 clear 或 blocked。
- `operation_plan`：一次按业务顺序排列的联动调整，包含多个阶段及计划状态。
- `plan_stage`：计划中的一个有序阶段，记录目标闸门、目标开度、绑定命令和阶段状态。

## 业务流程

### register-gate

运行协调员通过 `POST /gates` 登记闸门。服务校验标识、名称、最大开度和初始开度，写入空闲状态；重复标识返回稳定冲突错误，不覆盖原记录。

### request-gate-command

运行协调员通过 `POST /commands` 提交开度调整命令。服务读取闸门当前开度和限制，校验目标开度与申请信息，创建待复核命令并冻结起始开度。

### review-safety-interlock

安全复核员通过 `POST /commands/{id}/interlock` 提交复核结论。clear 结论会原子占用闸门并签发一次执行令牌；blocked 结论会把命令置为 rejected。其他命令先占用同一闸门时，当前 clear 结论返回冲突且不改变闸门。

### execute-gate-command

闸门操作员通过 `POST /commands/{id}/execute` 提交执行令牌。服务校验命令、令牌、闸门占用关系和目标开度，在临界区内完成 approved、executing、completed 或 aborted 的转换，并同步更新闸门状态。令牌在命令结束后失效，不能复用。绑定到联动计划的命令不能通过此入口执行，必须走计划阶段执行入口。

### create-operation-plan

运行协调员通过 `POST /plans` 提交有序阶段清单。服务校验计划标识、阶段数量、每个阶段的闸门引用、目标开度边界和原因。计划登记后所有阶段均为 waiting，只保存意图：不创建命令、不占用任何闸门；后续阶段的闸门在计划登记时必须存在且未故障。

### advance-plan-stage

运行协调员通过 `POST /plans/{id}/stages/{sequence}/advance` 启动一个阶段。服务在仓库临界区内原子完成：

1. 校验计划未 halted/completed、阶段处于 waiting，且所有更早阶段均已完成；越级、重复推进返回稳定冲突。
2. 读取上一阶段绑定命令的真实记录，确认其状态为 completed；只信任命令事实，不信任缓存状态。
3. 重新读取目标闸门的当前条件：存在、未故障、idle 且无活动命令，并按当前开度与最大开度校验目标开度；此刻才冻结起始开度。
4. 创建 pending 命令、应用 clear 联锁结论、签发执行令牌并占用闸门，同时把阶段置为 armed 并绑定命令。任一步失败则命令、闸门、阶段都不写入。

### execute-plan-stage

闸门操作员通过 `POST /plans/{id}/stages/{sequence}/execute` 执行 armed 阶段。服务校验计划状态、阶段状态、命令与计划的绑定关系、令牌和闸门占用，在同一临界区完成命令与闸门转换并落阶段结果：成功则阶段 completed，最后一个阶段完成后计划 completed；故障则命令 aborted、闸门 faulted、阶段 failed 且计划 halted。已完成阶段不被回写，halted 后任何阶段都不能再推进，waiting 阶段的闸门保持空闲。

## 状态与规则

- `gate` 状态：idle、reserved、moving、faulted。
- `gate_command` 状态：pending、approved、rejected、executing、completed、aborted。
- `operation_plan` 状态：active、completed、halted。
- `plan_stage` 状态：waiting、armed、completed、failed。
- 只有 pending 命令可以复核。
- 只有 approved 命令可以执行，且执行令牌必须匹配。
- clear 复核与闸门占用必须同时成功；任一步失败时两者都不写入。
- blocked 复核使命令 rejected，不会占用闸门。
- 模拟故障会令命令 aborted、闸门 faulted，并保留最终开度。
- 已完成或已中止的命令不能再次执行。
- 阶段只能按序号顺序启动：上一阶段命令未真实完成时，后续阶段返回 `predecessor_not_completed`。
- 阶段启动时按当前闸门条件重新确认：占用中或故障返回 `gate_not_idle`/`gate_faulted`，起始开度以启动时刻为准。
- 阶段 armed、命令 approved 与闸门 reserved 在同一临界区提交；阶段结果、命令终态与闸门状态也在同一临界区提交。
- 重复推进 armed/completed/failed 阶段分别返回 `stage_already_armed`、`stage_already_completed`、`plan_halted`。
- 计划 halted 后任何推进或执行都返回 `plan_halted`；未开始阶段不创建命令、不占用闸门。

## 模块边界

- `cmd/server`：进程入口、监听地址和退出控制。
- `internal/httpapi`：HTTP 路由、JSON 解码、状态码映射。
- `internal/coordination`：工作流编排、命令标识和执行令牌签发。
- `internal/domain`：实体、不变量和状态转换规则。
- `internal/storage`：带互斥保护的内存仓库和原子写入。
- `internal/contracts`：API 请求、响应和稳定错误结构。
- `internal/bootstrap`：运行时依赖装配。

## 接口

- `GET /health`
- `POST /gates`
- `GET /gates/{id}`
- `POST /commands`
- `GET /commands/{id}`
- `POST /commands/{id}/interlock`
- `POST /commands/{id}/execute`
- `POST /plans`
- `GET /plans/{id}`
- `POST /plans/{id}/stages/{sequence}/advance`
- `POST /plans/{id}/stages/{sequence}/execute`

错误响应统一为：

```json
{"code":"stable_error_code","message":"可读说明"}
```

## 验证计划

每条工作流通过 `cmd/workflowcheck` 启动真实 HTTP 服务并调用公开接口，校验成功状态、持久化结果和关键失败分支。联动计划工作流覆盖：越级与重复推进、前置未完成、条件变化下的重新确认、并发占用冲突、故障中止与计划 halted、已完成阶段不被回写、未开始阶段不占用闸门。
